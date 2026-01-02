// Package ociwalk provides a streaming, tar-like reader over the merged
// filesystem view of an OCI image stored in OCI layout format.
//
// Design goals:
//   - Minimal dependencies (std + opencontainers specs only)
//   - Streaming iteration similar to archive/tar.Reader
//   - Correct handling of layer order and whiteouts
//   - Deterministic behavior suitable for reproducible builds
//
// Non-goals (by design, but extensible):
//   - Applying permissions/ownership to a real filesystem
//   - Handling non-tar layer media types
//   - Overlayfs opaque directories beyond OCI whiteout semantics
package oci

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Reader behaves similarly to archive/tar.Reader, but iterates over the
// merged filesystem view of an OCI image reference.
//
// Usage:
//
//	r, _ := ociwalk.Open(layoutDir, "example.com/foo:latest")
//	for {
//	    hdr, err := r.Next()
//	    if err == io.EOF { break }
//	    if err != nil { return err }
//	    io.Copy(dst, r)
//	}
type Reader struct {
	layers []*layerReader
	seen   map[string]struct{}
	opaque map[string]struct{}

	cur *layerReader
}

// Open opens an OCI layout directory and returns a Reader over the given reference.
func Open(layoutDir string) (*Reader, error) {
	idx, err := loadIndex(layoutDir)
	if err != nil {
		return nil, err
	}

	manifestDesc, err := findManifest(idx)
	if err != nil {
		return nil, err
	}

	manifest, err := loadManifest(layoutDir, manifestDesc)
	if err != nil {
		return nil, err
	}

	// Layers are applied from base -> top, but read in reverse so that
	// topmost entries win.
	var layers []*layerReader
	for i := len(manifest.Layers) - 1; i >= 0; i-- {
		lr, err := openLayer(layoutDir, manifest.Layers[i])
		if err != nil {
			return nil, err
		}
		layers = append(layers, lr)
	}

	return &Reader{
		layers: layers,
		seen:   make(map[string]struct{}),
		opaque: make(map[string]struct{}),
	}, nil
}

// Next advances to the next visible file entry.
func (r *Reader) Next() (*tar.Header, error) {
	for {
		if r.cur == nil {
			if len(r.layers) == 0 {
				return nil, io.EOF
			}
			r.cur = r.layers[0]
			r.layers = r.layers[1:]
		}

		hdr, err := r.cur.Next()
		if err == io.EOF {
			r.cur.Close()
			r.cur = nil
			continue
		}
		if err != nil {
			return nil, err
		}

		name := cleanPath(hdr.Name)

		// Opaque directory whiteout handling (.wh..wh..opq)
		if path.Base(name) == ".wh..wh..opq" {
			dir := path.Dir(name)
			r.opaque[dir] = struct{}{}
			continue
		}

		// Whiteout handling (.wh.<name>)
		base := path.Base(name)
		if after, ok := strings.CutPrefix(base, ".wh."); ok {
			target := path.Join(path.Dir(name), after)
			r.seen[target] = struct{}{}
			continue
		}

		// Suppress entries hidden by opaque directories
		for d := range r.opaque {
			if name == d || strings.HasPrefix(name, d+"/") {
				goto skip
			}
		}

		if _, ok := r.seen[name]; ok {
			continue
		}

		r.seen[name] = struct{}{}
		hdr.Name = name
		return hdr, nil
	skip:
		continue
	}
}

// Read reads from the current file entry.
func (r *Reader) Read(p []byte) (int, error) {
	if r.cur == nil {
		return 0, io.EOF
	}
	return r.cur.Read(p)
}

// --- Internal helpers ---

type layerReader struct {
	closer io.Closer
	tr     *tar.Reader
}

func openLayer(layoutDir string, desc specs.Descriptor) (*layerReader, error) {
	if desc.MediaType != specs.MediaTypeImageLayerGzip &&
		desc.MediaType != specs.MediaTypeImageLayer {
		return nil, fmt.Errorf("unsupported layer media type: %s", desc.MediaType)
	}

	blobPath := filepath.Join(layoutDir, "blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded())
	f, err := os.Open(blobPath)
	if err != nil {
		return nil, err
	}

	var r io.Reader = f
	if desc.MediaType == specs.MediaTypeImageLayerGzip {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		r = gz
		return &layerReader{closer: multiCloser{gz, f}, tr: tar.NewReader(r)}, nil
	}

	return &layerReader{closer: f, tr: tar.NewReader(r)}, nil
}

func (l *layerReader) Next() (*tar.Header, error) {
	return l.tr.Next()
}

func (l *layerReader) Read(p []byte) (int, error) {
	return l.tr.Read(p)
}

func (l *layerReader) Close() error {
	return l.closer.Close()
}

// --- OCI parsing ---

func loadIndex(layoutDir string) (*specs.Index, error) {
	b, err := os.ReadFile(filepath.Join(layoutDir, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx specs.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func findManifest(idx *specs.Index) (specs.Descriptor, error) {
	for _, m := range idx.Manifests {
		return m, nil
	}
	return specs.Descriptor{}, fmt.Errorf("no manifests in index")
}

func loadManifest(layoutDir string, desc specs.Descriptor) (*specs.Manifest, error) {
	if desc.MediaType != specs.MediaTypeImageManifest {
		return nil, errors.New("descriptor is not an image manifest")
	}
	blobPath := filepath.Join(layoutDir, "blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded())
	b, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, err
	}
	var m specs.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// --- Utilities ---

func cleanPath(p string) string {
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "/")
	return p
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var errs []string
	for _, c := range m {
		if err := c.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// DeterministicDigestOrder sorts descriptors by digest for reproducibility
// if a caller needs stable iteration outside OCI ordering.
func DeterministicDigestOrder(ds []specs.Descriptor) {
	sort.Slice(ds, func(i, j int) bool {
		return ds[i].Digest.String() < ds[j].Digest.String()
	})
}

var _ io.Reader = (*Reader)(nil)
