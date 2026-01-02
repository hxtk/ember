package cpio

import (
	"archive/tar"
)

// Standard Unix file type bits (S_IFMT)
const (
	s_IFLNK = 0xa000
	s_IFREG = 0x8000
	s_IFBLK = 0x6000
	s_IFDIR = 0x4000
	s_IFCHR = 0x2000
	s_IFIFO = 0x1000
)

// HeaderFromTar converts a tar.Header to a cpio.Header.
// It maps the file mode, ownership, and device numbers.
// Note: CPIO 'newc' format handles file names differently (no separate prefix),
// so this joins the Name to the Header.
func HeaderFromTar(th *tar.Header, inode int) *Header {
	// 1. Basic Fields
	h := &Header{
		Name:     th.Name,
		Uid:      th.Uid,
		Gid:      th.Gid,
		Size:     th.Size,
		ModTime:  th.ModTime,
		DevMajor: int(th.Devmajor),
		DevMinor: int(th.Devminor),
	}

	// 2. Translate File Type (Typeflag -> Mode bits)
	// We start with the permission bits from the tar header
	// (masking out any high bits just in case, though usually 0777)
	mode := int64(th.Mode & 07777)

	switch th.Typeflag {
	case tar.TypeDir:
		mode |= s_IFDIR
		// Directories in CPIO usually have 0 size
		h.Size = 0
	case tar.TypeSymlink:
		mode |= s_IFLNK
		h.Size = int64(len(th.Linkname)) // CPIO stores link target as content
	case tar.TypeChar:
		mode |= s_IFCHR
		h.Size = 0 // Device files have 0 size
	case tar.TypeBlock:
		mode |= s_IFBLK
		h.Size = 0
	case tar.TypeFifo:
		mode |= s_IFIFO
		h.Size = 0
	case tar.TypeReg, tar.TypeRegA:
		mode |= s_IFREG
	// Hard links are special in Tar (they share content).
	// In CPIO, they are just files with the same Inode number.
	// You must handle inode mapping externally if you want true hard links.
	case tar.TypeLink:
		mode |= s_IFREG
		h.Size = 0 // Hard links in tar usually have size 0 in the header
	}

	h.Mode = mode

	// 3. Handle Hard Links vs Symlinks
	// For symlinks, Tar stores the target in Linkname.
	// CPIO treats symlinks as file content, so the writer must write `th.Linkname`
	// to the body of the CPIO entry.
	// The caller of this function needs to be aware of this.

	// 4. Inodes
	// Tar doesn't strictly require Inodes, but CPIO relies on them for hardlink detection.
	// If the tar header doesn't have a specific Inode, we might default to 0
	// or let the caller assign a unique one.
	// Using a simple hash or counter is common if th.Xattrs["inode"] is missing.
	h.Inode = inode

	return h
}
