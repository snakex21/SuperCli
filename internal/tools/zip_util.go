package tools

import (
	"archive/zip"
	"fmt"
	"io"
)

// readZipEntry opens the zip at path and reads
// the bytes of a single entry by name. Returns
// an error if the zip is malformed, the entry is
// missing, or the entry's declared size exceeds
// maxBytes (with a runtime +1 cap as a safety net
// for malicious zips that lie about their size).
//
// This helper is used by every file-format tool
// in this package that needs one entry from a
// zip archive (F19 read_docx, F22 read_xlsx).
// It is deliberately conservative: it only
// opens the entry the caller asks for, never
// iterates the whole archive, and never writes
// to disk.
func readZipEntry(path, entryName string, maxBytes int64) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	var target *zip.File
	for _, f := range zr.File {
		if f.Name == entryName {
			target = f
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("entry %q not found", entryName)
	}
	// Cap on the declared uncompressed size.
	// This is a HINT (zip can lie); we also
	// cap at read time with io.LimitReader.
	if int64(target.UncompressedSize64) > maxBytes {
		return nil, fmt.Errorf("entry %q is too large: %d bytes (declared) > %d cap", entryName, target.UncompressedSize64, maxBytes)
	}
	rc, err := target.Open()
	if err != nil {
		return nil, fmt.Errorf("open entry: %w", err)
	}
	defer rc.Close()
	// Read with a +1 cap so a malicious zip that
	// lies about its size still gets bounded.
	limited := io.LimitReader(rc, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read entry: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("entry %q exceeded %d bytes during read", entryName, maxBytes)
	}
	return data, nil
}
