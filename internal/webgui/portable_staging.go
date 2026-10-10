package webgui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var errPortableStaging = errors.New("prepare portable staging")

// portableWorkDir keeps temporary application data inside the configured
// data directory. Export uses an allow-list, so this directory is never
// recursively included in its own archive. There is no OS-temp fallback.
func portableWorkDir(dataDir, pattern string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("%w: empty application data directory", errPortableStaging)
	}
	root := filepath.Join(dataDir, ".supercli", "staging")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("%w: %w", errPortableStaging, err)
	}
	path, err := os.MkdirTemp(root, pattern)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errPortableStaging, err)
	}
	return path, nil
}

type stagedUpload struct {
	Path string
	Name string
	Size int64
}

// stageMultipartUpload streams each candidate directly to private portable
// storage. Names define priority, preserving the existing file/document/files
// selection irrespective of multipart order. Only the first file per name is
// kept, and the caller's MaxBytesReader bounds the whole request.
func stageMultipartUpload(r *http.Request, dataDir, pattern string, maxBytes int64, names ...string) (stagedUpload, func(), error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return stagedUpload{}, nil, fmt.Errorf("invalid upload: %w", err)
	}
	stage, err := portableWorkDir(dataDir, pattern)
	if err != nil {
		return stagedUpload{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	seen := make([]bool, len(names))
	var best stagedUpload
	bestPriority := len(names)
	parts, headers := 0, 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return stagedUpload{}, nil, fmt.Errorf("read upload: %w", err)
		}
		parts++
		for _, values := range part.Header {
			headers += len(values)
		}
		// Match the default ReadForm limits removed by using a streaming
		// reader. Include ignored/duplicate parts in this resource budget.
		if parts > 1000 || headers > 10000 {
			return stagedUpload{}, nil, errors.New("multipart upload has too many parts or headers")
		}
		priority := len(names)
		for i, candidate := range names {
			if part.FormName() == candidate {
				priority = i
				break
			}
		}
		if priority == len(names) || part.FileName() == "" || seen[priority] || priority > bestPriority {
			if err := discardUploadPart(part); err != nil {
				return stagedUpload{}, nil, fmt.Errorf("read upload field: %w", err)
			}
			continue
		}
		seen[priority] = true
		target, err := os.CreateTemp(stage, "upload-*")
		if err != nil {
			return stagedUpload{}, nil, fmt.Errorf("%w: %w", errPortableStaging, err)
		}
		written, copyErr := io.Copy(target, io.LimitReader(part, maxBytes+1))
		closeErr := target.Close()
		if copyErr != nil {
			return stagedUpload{}, nil, fmt.Errorf("read upload: %w", copyErr)
		}
		if closeErr != nil {
			return stagedUpload{}, nil, fmt.Errorf("%w: %w", errPortableStaging, closeErr)
		}
		// A lower-priority candidate may exceed the limit while a later
		// preferred file is valid. Bound its staged prefix, drain the rest
		// under MaxBytesReader, and validate only the selected candidate.
		if err := discardUploadPart(part); err != nil {
			return stagedUpload{}, nil, fmt.Errorf("read upload: %w", err)
		}
		if best.Path != "" {
			if err := os.Remove(best.Path); err != nil {
				return stagedUpload{}, nil, fmt.Errorf("%w: %w", errPortableStaging, err)
			}
		}
		best = stagedUpload{Path: target.Name(), Name: part.FileName(), Size: written}
		bestPriority = priority
	}
	if best.Path == "" {
		return stagedUpload{}, nil, errors.New("no file uploaded")
	}
	if best.Size > maxBytes {
		return stagedUpload{}, nil, fmt.Errorf("uploaded file exceeds the %d-byte limit", maxBytes)
	}
	keep = true
	return best, cleanup, nil
}

func discardUploadPart(part io.ReadCloser) error {
	_, err := io.Copy(io.Discard, part)
	return errors.Join(err, part.Close())
}

func uploadErrorStatus(err error) int {
	if errors.Is(err, errPortableStaging) {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}
