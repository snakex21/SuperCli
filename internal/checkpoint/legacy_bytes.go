package checkpoint

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrLegacyByteLoss = errors.New("legacy checkpoint did not preserve original file bytes")

// Old git-add snapshots may contain normalized text or LFS pointers. Executing
// their old clean filter again can write gigabytes or run arbitrary commands,
// and cannot recover bytes that were never stored. Diagnose proven conversion
// without changing files; ordinary legacy blobs remain fully usable.
func (m *Manager) legacyByteLoss(ctx context.Context, path, expected, current string) error {
	if expected == "" || current == "" || expected == current {
		return nil
	}
	if normalized, err := m.normalizedCRLFHash(ctx, path); err != nil {
		return err
	} else if normalized == expected {
		return fmt.Errorf("%w: %q was stored with normalized line endings; exact undo is unavailable and workspace files were not changed", ErrLegacyByteLoss, path)
	}
	sizeOut, err := m.git(ctx, "cat-file", "-s", expected)
	if err != nil {
		return nil // The normal conflict path reports unavailable/mismatched blobs.
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil || size > 1024 {
		return nil
	}
	data, err := m.git(ctx, "cat-file", "blob", expected)
	if err == nil && strings.HasPrefix(data, "version https://git-lfs.github.com/spec/v1\n") && strings.Contains(data, "\noid sha256:") {
		return fmt.Errorf("%w: %q was stored as a Git LFS pointer; exact undo is unavailable and workspace files were not changed", ErrLegacyByteLoss, path)
	}
	return nil
}

func (m *Manager) normalizedCRLFHash(ctx context.Context, path string) (string, error) {
	input, err := os.Open(filepath.Join(m.home, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSnapshotFileBytes {
		return "", nil
	}
	var count byteCounter
	changed, err := copyWithoutCRLF(ctx, &count, io.LimitReader(input, info.Size()+1))
	if err != nil || !changed {
		return "", err
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha1.New()
	if _, err := fmt.Fprintf(hash, "blob %d%c", int64(count), 0); err != nil {
		return "", err
	}
	if _, err := copyWithoutCRLF(ctx, hash, io.LimitReader(input, info.Size()+1)); err != nil {
		return "", err
	}
	after, err := input.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", errors.New("file changed during legacy checkpoint comparison")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type byteCounter int64

func (c *byteCounter) Write(p []byte) (int, error) {
	*c += byteCounter(len(p))
	return len(p), nil
}

func copyWithoutCRLF(ctx context.Context, writer io.Writer, reader io.Reader) (bool, error) {
	input, output := make([]byte, 32<<10), make([]byte, 0, (32<<10)+1)
	pendingCR, changed := false, false
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, readErr := reader.Read(input)
		output = output[:0]
		for _, b := range input[:n] {
			if pendingCR {
				if b != '\n' {
					output = append(output, '\r')
				} else {
					changed = true
				}
				pendingCR = false
			}
			if b == '\r' {
				pendingCR = true
			} else {
				output = append(output, b)
			}
		}
		if errors.Is(readErr, io.EOF) && pendingCR {
			output = append(output, '\r')
			pendingCR = false
		}
		if _, err := writer.Write(output); err != nil {
			return false, err
		}
		if errors.Is(readErr, io.EOF) {
			return changed, nil
		}
		if readErr != nil {
			return false, readErr
		}
	}
}
