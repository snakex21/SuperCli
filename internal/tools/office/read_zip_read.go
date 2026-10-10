package office

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	zipReadMaxArchives      = 16
	zipReadMaxMatches       = 16
	zipReadDefaultTextBytes = 32 * 1024
	zipReadMaxTextBytes     = 64 * 1024
	zipReadMaxOutputBytes   = 80 * 1024
)

type zipTextMatch struct {
	archive      string
	archiveIndex int
	file         *zip.File
	header       string
}

// Plan every match and its byte budget before opening any entry. An overbroad
// pattern must not decompress a large archive, consume unbounded output, or
// silently hide matches behind a preview. Archive parts are always explicit.
func (t *ReadZipTool) readAction(ctx context.Context, single string, paths []string, pattern string, maxEntries, maxMatches, maxTextBytes int) (Result, error) {
	fail := func(err error) (Result, error) { return Result{Err: err}, err }
	if strings.TrimSpace(pattern) == "" {
		return fail(fmt.Errorf("read_zip: read requires a nonempty pattern"))
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return fail(fmt.Errorf("read_zip: invalid read pattern: %w", err))
	}
	if single != "" {
		if len(paths) > 0 {
			return fail(fmt.Errorf("read_zip: read requires path or paths, not both"))
		}
		paths = []string{single}
	}
	if len(paths) == 0 || len(paths) > zipReadMaxArchives {
		return fail(fmt.Errorf("read_zip: read requires 1-%d explicitly named archives", zipReadMaxArchives))
	}
	if maxMatches == 0 {
		maxMatches = zipReadMaxMatches
	}
	if maxMatches < 1 || maxMatches > zipReadMaxMatches {
		return fail(fmt.Errorf("read_zip: max_matches must be 1-%d", zipReadMaxMatches))
	}
	if maxTextBytes == 0 {
		maxTextBytes = zipReadDefaultTextBytes
	}
	if maxTextBytes < 1 || maxTextBytes > zipReadMaxTextBytes {
		return fail(fmt.Errorf("read_zip: max_text_bytes must be 1-%d", zipReadMaxTextBytes))
	}
	entryLimit := t.MaxEntries
	if entryLimit <= 0 {
		entryLimit = DefaultMaxZipEntries
	}
	if maxEntries <= 0 {
		maxEntries = entryLimit
	}
	if maxEntries > entryLimit {
		return fail(fmt.Errorf("read_zip: read max_entries cannot exceed %d", entryLimit))
	}

	var matches []zipTextMatch
	var archiveBytes, textBytes int64
	var entryCount, outputBytes int
	seen := make(map[string]bool, len(paths))
	for archiveIndex, archive := range paths {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if strings.TrimSpace(archive) == "" {
			return fail(fmt.Errorf("read_zip: archive paths must be nonempty"))
		}
		full, err := resolveSandboxed(t.BaseDir, archive)
		if err != nil {
			return fail(fmt.Errorf("read_zip: archive %q: %w", archive, err))
		}
		identity := full
		if runtime.GOOS == "windows" {
			identity = strings.ToLower(identity)
		}
		if seen[identity] {
			return fail(fmt.Errorf("read_zip: duplicate archive %q", archive))
		}
		seen[identity] = true
		info, err := os.Stat(full)
		if err != nil {
			return fail(fmt.Errorf("read_zip: archive %q: %w", archive, err))
		}
		if info.IsDir() {
			return fail(fmt.Errorf("read_zip: archive %q is a directory", archive))
		}
		if info.Size() > t.MaxZipBytes-archiveBytes {
			return fail(fmt.Errorf("read_zip: read archives exceed %d bytes on disk", t.MaxZipBytes))
		}
		archiveBytes += info.Size()
		reader, err := zip.OpenReader(full)
		if err != nil {
			return fail(fmt.Errorf("read_zip: open archive %q: %w", archive, err))
		}
		defer reader.Close()
		if len(reader.File) > maxEntries-entryCount {
			return fail(fmt.Errorf("read_zip: read archives exceed %d entries", maxEntries))
		}
		entryCount += len(reader.File)
		for _, file := range reader.File {
			if file.FileInfo().IsDir() || !matchEntry(file.Name, pattern) {
				continue
			}
			if len(matches) >= maxMatches {
				return fail(fmt.Errorf("read_zip: read pattern %q exceeds %d matched files; narrow pattern", pattern, maxMatches))
			}
			// Compare uint64 sizes before converting: a forged header must not
			// wrap negative and escape a byte budget.
			if file.UncompressedSize64 > uint64(maxTextBytes)-uint64(textBytes) {
				return fail(fmt.Errorf("read_zip: matching text exceeds %d bytes; narrow pattern or raise max_text_bytes (maximum %d)", maxTextBytes, zipReadMaxTextBytes))
			}
			if t.MaxSingleFileBytes <= 0 || file.UncompressedSize64 > uint64(t.MaxSingleFileBytes) {
				return fail(fmt.Errorf("read_zip: archive %q entry %q exceeds per-file cap", archive, file.Name))
			}
			size := int64(file.UncompressedSize64)
			header := fmt.Sprintf("== archive=%q entry=%q (%d bytes) ==\n", archive, file.Name, size)
			if len(header)+int(size)+1 > zipReadMaxOutputBytes-outputBytes {
				return fail(fmt.Errorf("read_zip: matching text and entry names exceed %d output bytes; narrow pattern", zipReadMaxOutputBytes))
			}
			textBytes += size
			outputBytes += len(header) + int(size) + 1
			matches = append(matches, zipTextMatch{archive: archive, archiveIndex: archiveIndex, file: file, header: header})
		}
	}
	if len(matches) == 0 {
		return Result{Text: fmt.Sprintf("(no entries match %q across %d archives)", pattern, len(paths))}, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].archiveIndex != matches[j].archiveIndex {
			return matches[i].archiveIndex < matches[j].archiveIndex
		}
		return matches[i].file.Name < matches[j].file.Name
	})
	var output strings.Builder
	output.Grow(outputBytes)
	for _, match := range matches {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		reader, err := match.file.Open()
		if err != nil {
			return fail(fmt.Errorf("read_zip: archive %q entry %q: %w", match.archive, match.file.Name, err))
		}
		size := int64(match.file.UncompressedSize64)
		data, err := io.ReadAll(io.LimitReader(reader, size+1))
		closeErr := reader.Close()
		if err != nil {
			return fail(fmt.Errorf("read_zip: archive %q entry %q: %w", match.archive, match.file.Name, err))
		}
		if closeErr != nil {
			return fail(fmt.Errorf("read_zip: close archive %q entry %q: %w", match.archive, match.file.Name, closeErr))
		}
		if int64(len(data)) != size {
			return fail(fmt.Errorf("read_zip: archive %q entry %q has an invalid declared size", match.archive, match.file.Name))
		}
		if !zipUTF8Text(data) {
			return fail(fmt.Errorf("read_zip: archive %q entry %q is not UTF-8 text; binary and encoded output are not supported", match.archive, match.file.Name))
		}
		output.WriteString(match.header)
		output.Write(data)
		output.WriteByte('\n')
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return Result{Text: output.String()}, nil
}

func zipUTF8Text(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for _, r := range string(data) {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}
