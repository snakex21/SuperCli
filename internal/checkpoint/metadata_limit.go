package checkpoint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// Records share the collector's metadata bound. Oversized legacy files are
// refused and preserved; no reader/admission may silently turn them into []
// or automatically migrate them. Failed turn admissions retain recovery pins.
var ErrMetadataLimit = errors.New("checkpoint metadata limit exceeded")

func checkCheckpointMetadataBytes(n int64) error {
	if n < 0 {
		return ErrStoreInventory
	}
	if n > retentionMaxMetadata {
		return fmt.Errorf("%w: %d bytes exceed %d byte maximum; checkpoint metadata is preserved", ErrMetadataLimit, n, retentionMaxMetadata)
	}
	return nil
}

func readCheckpointMetadata(path string) ([]byte, error) {
	return readCheckpointMetadataWithOpen(path, os.Open)
}

// The injectable open is only a bounded-read test seam; no callback survives
// this call. Refuse an oversized regular file before opening/allocating at all.
func readCheckpointMetadataWithOpen(path string, open func(string) (*os.File, error)) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrStoreInventory
	}
	if err := checkCheckpointMetadataBytes(info.Size()); err != nil {
		return nil, err
	}
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, ErrStoreInventory
	}
	if err := checkCheckpointMetadataBytes(opened.Size()); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, retentionMaxMetadata+1))
	if err != nil {
		return nil, err
	}
	if err := checkCheckpointMetadataBytes(int64(len(data))); err != nil {
		return nil, err
	}
	return data, nil
}

// Count only encoded string contents, omitting quotes, keys and whitespace.
// This is a lower bound, never an estimate authorizing a write. On success it
// allocates nothing; stop at the shared maximum before accumulating overflow.
// MarshalIndent's actual byte length remains the final admission check.
func checkpointMetadataStringsLowerBound(records []Record) (int64, error) {
	var total int64
	for i := range records {
		r := &records[i]
		for _, s := range [...]string{r.ID, r.CompletionKey, r.SessionID, r.Prompt, r.Before, r.After} {
			var err error
			total, err = addCheckpointJSONStringBytes(total, s)
			if err != nil {
				return total, err
			}
		}
		for _, s := range r.Files {
			var err error
			total, err = addCheckpointJSONStringBytes(total, s)
			if err != nil {
				return total, err
			}
		}
		for _, change := range r.Changes {
			var err error
			total, err = addCheckpointJSONStringBytes(total, change.Path)
			if err != nil {
				return total, err
			}
			total, err = addCheckpointJSONStringBytes(total, change.Kind)
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

// encoding/json's default HTML escaping is used by MarshalIndent. UTF-8
// replacement and U+2028/U+2029 escaping are counted without converting s.
func addCheckpointJSONStringBytes(total int64, s string) (int64, error) {
	if total < 0 || total > retentionMaxMetadata {
		return total, ErrMetadataLimit
	}
	if int64(len(s)) > retentionMaxMetadata-total {
		return total, ErrMetadataLimit
	}
	total += int64(len(s))
	for i := 0; i < len(s); {
		var extra int64
		if b := s[i]; b < utf8.RuneSelf {
			switch b {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				extra = 1
			case '<', '>', '&':
				extra = 5
			default:
				if b < 0x20 {
					extra = 5
				}
			}
			i++
		} else {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				extra = 5 // One invalid byte becomes six ASCII bytes: \ufffd.
			} else if r == '\u2028' || r == '\u2029' {
				extra = 3 // Three UTF-8 bytes become six ASCII escape bytes.
			}
			i += size
		}
		if extra > retentionMaxMetadata-total {
			return total, ErrMetadataLimit
		}
		total += extra
	}
	return total, nil
}
