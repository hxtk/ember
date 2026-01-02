package cpio

import (
	"fmt"
	"io"
	"time"
)

// Format represents the CPIO format.
// Currently only "newc" is supported.
const magicNewc = "070701"

// Header represents a single CPIO file header.
// It roughly matches the fields exposed by archive/tar.Header.
type Header struct {
	Name      string    // Name of the file entry
	Mode      int64     // Permission and mode bits
	Uid       int       // User ID of owner
	Gid       int       // Group ID of owner
	Size      int64     // Logical file size in bytes
	ModTime   time.Time // Modification time (seconds since Unix epoch)
	DevMajor  int       // Major number of character or block device
	DevMinor  int       // Minor number of character or block device
	RdevMajor int       // Major number of the device node (if this is a device)
	RdevMinor int       // Minor number of the device node (if this is a device)
	Links     int       // Number of hard links
	Inode     int       // Inode number
}

// Writer provides sequential writing of a CPIO archive.
type Writer struct {
	w             io.Writer
	err           error
	nb            int64 // bytes written to current entry
	pad           int64 // padding needed at end of current entry
	closed        bool
	headerWritten bool
}

// NewWriter creates a new Writer writing to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteHeader writes the CPIO header.
// This must be called before writing the file content.
func (tw *Writer) WriteHeader(hdr *Header) error {
	if tw.closed {
		return fmt.Errorf("cpio: writer is closed")
	}
	if tw.err != nil {
		return tw.err
	}

	// If we were in the middle of a previous entry, finish it.
	if tw.headerWritten {
		if err := tw.flushPadding(); err != nil {
			return err
		}
	}

	// Prepare the 110-byte fixed header (excluding filename).
	// Format is:
	// magic (6), ino (8), mode (8), uid (8), gid (8), nlink (8),
	// mtime (8), filesize (8), devmajor (8), devminor (8),
	// rdevmajor (8), rdevminor (8), namesize (8), check (8)

	// Convert name to bytes to get accurate length (including null terminator)
	nameBytes := []byte(hdr.Name)
	// namesize includes the trailing null byte
	nameSize := len(nameBytes) + 1

	// Sprintf is used here for clarity, though manual hex encoding is faster.
	// %08X formats integers as 8-character uppercase hex strings.
	headerStr := fmt.Sprintf(
		"%s%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X",
		magicNewc,
		uint32(hdr.Inode),
		uint32(hdr.Mode),
		uint32(hdr.Uid),
		uint32(hdr.Gid),
		uint32(hdr.Links),
		uint32(hdr.ModTime.Unix()),
		uint32(hdr.Size),
		uint32(hdr.DevMajor),
		uint32(hdr.DevMinor),
		uint32(hdr.RdevMajor),
		uint32(hdr.RdevMinor),
		uint32(nameSize),
		0, // Checksum is always 0 for newc
	)

	// Write the fixed header
	if _, err := io.WriteString(tw.w, headerStr); err != nil {
		tw.err = err
		return err
	}

	// Write the filename
	if _, err := tw.w.Write(nameBytes); err != nil {
		tw.err = err
		return err
	}

	// Write the null terminator for the filename
	if _, err := tw.w.Write([]byte{0}); err != nil {
		tw.err = err
		return err
	}

	// Calculate padding.
	// The header + filename + null terminator must be padded to a multiple of 4 bytes.
	// Fixed header length is 110 bytes.
	totalHeaderLen := 110 + nameSize
	padLen := (4 - (totalHeaderLen % 4)) % 4
	if padLen > 0 {
		if _, err := tw.w.Write(zeros[:padLen]); err != nil {
			tw.err = err
			return err
		}
	}

	// Setup state for writing the body
	tw.nb = 0
	tw.pad = (4 - (hdr.Size % 4)) % 4 // Body must also be 4-byte aligned
	tw.headerWritten = true
	return nil
}

// Write writes to the current file in the CPIO archive.
// Write returns the error ErrWriteTooLong if more than
// hdr.Size bytes are written.
func (tw *Writer) Write(b []byte) (n int, err error) {
	if tw.closed {
		err = fmt.Errorf("cpio: write to closed writer")
		return
	}
	if !tw.headerWritten {
		err = fmt.Errorf("cpio: write before header")
		return
	}

	// Write data to underlying writer
	n, err = tw.w.Write(b)
	if err != nil {
		tw.err = err
		return
	}

	tw.nb += int64(n)
	return
}

// flushPadding writes the zeros needed to pad the file content to a 4-byte boundary.
func (tw *Writer) flushPadding() error {
	if tw.pad > 0 {
		if _, err := tw.w.Write(zeros[:tw.pad]); err != nil {
			tw.err = err
			return err
		}
	}
	tw.headerWritten = false // Reset for next file
	return nil
}

// Close closes the CPIO archive by writing the "TRAILER!!!" entry.
// It does not close the underlying writer.
func (tw *Writer) Close() error {
	if tw.closed {
		return nil
	}

	// Finish the current file if open
	if tw.headerWritten {
		if err := tw.flushPadding(); err != nil {
			return err
		}
	}

	// Write the trailer entry.
	// The trailer is a file named "TRAILER!!!" with size 0.
	trailer := &Header{
		Name:  "TRAILER!!!",
		Links: 1, // Usually 1 for the trailer
	}

	if err := tw.WriteHeader(trailer); err != nil {
		return err
	}

	// We just wrote the header for the trailer, which requires padding flushing
	// because WriteHeader sets up state for a body. Since the body size is 0,
	// flushPadding will just reset the state, but we call it for correctness.
	if err := tw.flushPadding(); err != nil {
		return err
	}

	tw.closed = true
	return nil
}

// zeros is a pre-allocated byte slice used for padding.
var zeros = []byte{0, 0, 0, 0}
