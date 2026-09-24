package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"supercli/internal/tools/fileops"
)

// fallback searches text independent of its filename/extension. The previous
// language whitelist missed Zig, Windows scripts and extensionless build files.
// Buffers belong to one search, so concurrent workers do not share mutable data.
func (s *SearchCode) fallback(ctx context.Context, root, query string, max int, previews ...*searchContext) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, nil
	}
	if _, err := os.Stat(root); err != nil {
		message := fileops.FileErr(err, root)
		if os.IsNotExist(err) && strings.Contains(root, ",") {
			message = fmt.Errorf("%w; path accepts one existing file or directory", message)
		}
		return Result{Err: fmt.Errorf("search_code: %w", message)}, nil
	}
	re, reErr := regexp.Compile(query)
	literal := strings.ToLower(query)
	reader := bufio.NewReaderSize(strings.NewReader(""), 32*1024)
	scanBuffer := make([]byte, 64*1024)
	count := 0
	var b strings.Builder
	walk := func(path string) error {
		if count >= max {
			return errStopWalk
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !searchFileIncluded(previews, root, path) {
			return nil
		}
		f, err := openFile(path)
		if err != nil {
			return fmt.Errorf("search_failed open: %w", fileops.FileErr(err, path))
		}
		defer f.Close()
		reader.Reset(f)
		head, err := reader.Peek(8192)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("search_failed read %s: %w", path, err)
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil
		} // binary/UTF-16, like rg's text search
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(scanBuffer, 1024*1024)
		lineNo := 0
		displayPath := ""
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			lineNo++
			line := scanner.Bytes()
			var hit bool
			if reErr == nil {
				hit = re.Match(line)
			} else {
				hit = strings.Contains(strings.ToLower(string(line)), literal)
			}
			if !hit {
				continue
			}
			if displayPath == "" {
				displayPath = s.displayPath(path)
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			text := string(line)
			fmt.Fprintf(&b, "%s:%d:%s", displayPath, lineNo, text)
			captureSearchHit(previews, path, lineNo, text)
			count++
			if count >= max {
				return errStopWalk
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("search_failed scan %s after line %d: %w", path, lineNo, err)
		}
		return nil
	}
	var descend func(string) bool
	if len(previews) > 0 && previews[0] != nil && previews[0].include != nil {
		glob := previews[0].include
		descend = func(dir string) bool { return glob.canDescend(root, dir) }
	}
	if err := walkFilesFiltered(ctx, root, descend, walk); err != nil && !errors.Is(err, errStopWalk) {
		return Result{Text: b.String(), Err: fmt.Errorf("search incomplete: %w", err)}, nil
	}
	if b.Len() == 0 {
		return Result{Text: "no matches"}, nil
	}
	if count >= max {
		return searchLimitedResult(b.String(), max, previews), nil
	}
	return Result{Text: b.String()}, nil
}

// Paths remain relative to the tool's workspace, never the narrower search
// root. An allowed external root keeps absolute paths for unambiguous reads.
func (s *SearchCode) displayPath(path string) string {
	base, err := filepath.Abs(s.WorkDir)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return path
	}
	return filepath.ToSlash(rel)
}

func (s *SearchCode) displaySearchLine(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j > i+1 && j < len(line) && line[j] == ':' {
			return s.displayPath(line[:i]) + line[i:]
		}
	}
	return line
}
