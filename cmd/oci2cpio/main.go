package main

import (
	"archive/tar"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/hxtk/ember/pkg/cpio"
	"github.com/hxtk/ember/pkg/oci"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <oci-layout-path> <reference>\n", os.Args[0])
		os.Exit(2)
	}

	layoutPath := os.Args[1]

	if err := run(layoutPath); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(layoutPath string) error {
	// Open OCI reader (handles layer merge + whiteouts internally)
	ociReader, err := oci.Open(layoutPath)
	if err != nil {
		return fmt.Errorf("open OCI layout: %w", err)
	}

	// Create CPIO writer targeting stdout
	cpioWriter := cpio.NewWriter(os.Stdout)
	defer func() {
		_ = cpioWriter.Close()
	}()

	inode := 1
	for {
		// Read next merged OCI entry
		hdr, err := ociReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read OCI entry: %w", err)
		}

		nlink := 1
		if hdr.Typeflag == tar.TypeDir {
			nlink = 2
		}

		xattrs := make(map[string][]byte, len(hdr.PAXRecords))
		for k, v := range hdr.PAXRecords {
			after, ok := strings.CutPrefix(k, "SCHILY.xattr.")
			if !ok {
				continue
			}
			xattrs[after] = []byte(v)
		}

		name := hdr.Name
		if hdr.Typeflag == tar.TypeDir {
			name += "/"
		}
		if !strings.HasPrefix(hdr.Name, "./") {
			name = "./" + name
		}

		// Translate OCI header → CPIO header
		cpioHdr := cpio.HeaderFromTar(hdr, inode)
		cpioHdr.Links = nlink
		inode++

		// Write CPIO header
		if err := cpioWriter.WriteHeader(cpioHdr); err != nil {
			return fmt.Errorf("write CPIO header for %q: %w", hdr.Name, err)
		}

		// Stream file payload (if any)
		if hdr.Size > 0 {
			if _, err := io.CopyN(cpioWriter, ociReader, hdr.Size); err != nil {
				return fmt.Errorf("copy payload for %q: %w", hdr.Name, err)
			}
		} else if hdr.Typeflag == tar.TypeSymlink {
			_, err := cpioWriter.Write([]byte(hdr.Linkname))
			if err != nil {
				return fmt.Errorf("write linkname for %q: %w", hdr.Name, err)
			}
		}
	}

	return nil
}
