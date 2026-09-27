package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	maxSuggestionEntries = 512
	maxReadSuggestions   = 3
)

// suggestReadFile is best-effort recovery context for a missing, already
// sandbox-resolved path. Inspect only its parent, never recurse or read an
// alternative file. Successful reads and other errors pay no directory I/O.
func suggestReadFile(ctx context.Context, path string, readErr error) error {
	if !errors.Is(readErr, fs.ErrNotExist) || ctx.Err() != nil {
		return readErr
	}
	name := strings.ToLower(filepath.Base(path))
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if len(stem) < 3 {
		return readErr
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return readErr
	}
	defer dir.Close()
	// A bounded read avoids sorting/loading a whole large directory. Suggestions
	// may be incomplete there; the original missing-file error remains authoritative.
	entries, listErr := dir.ReadDir(maxSuggestionEntries)
	var names, alternatives []string
	alternativeCount := 0
	for _, entry := range entries {
		if ctx.Err() != nil {
			return readErr
		}
		if !entry.Type().IsRegular() {
			continue
		}
		candidate := strings.ToLower(entry.Name())
		if filepath.Ext(candidate) != ext {
			continue
		}
		alternativeCount++
		if alternativeCount <= maxReadSuggestions {
			alternatives = append(alternatives, entry.Name())
		}
		candidateStem := strings.TrimSuffix(candidate, ext)
		if len(candidateStem) < 3 {
			continue
		}
		if strings.Contains(stem, candidateStem) || strings.Contains(candidateStem, stem) {
			names = append(names, entry.Name())
		}
	}
	label := "Similar files (same directory)"
	if len(names) == 0 {
		// If the same-extension choices fit completely, an exact directory hint
		// saves a separate listing even when the guessed basename has no match.
		// Never present an incomplete scan as a small set of alternatives.
		if alternativeCount == 0 || alternativeCount > maxReadSuggestions || len(entries) >= maxSuggestionEntries || (listErr != nil && !errors.Is(listErr, io.EOF)) {
			return readErr
		}
		names = alternatives
		sort.Strings(names)
		label = "Files with the same extension (same directory)"
	} else {
		// Prefer the closest length, with a stable tie-break, so a short base name
		// beats unrelated long variants when only three suggestions fit.
		distance := func(s string) int {
			n := len(s) - len(name)
			if n < 0 {
				n = -n
			}
			return n
		}
		sort.Slice(names, func(i, j int) bool {
			di, dj := distance(names[i]), distance(names[j])
			if di != dj {
				return di < dj
			}
			return names[i] < names[j]
		})
		if len(names) > maxReadSuggestions {
			names = names[:maxReadSuggestions]
		}
	}
	for i := range names {
		names[i] = strconv.Quote(names[i])
	}
	return fmt.Errorf("%w\n%s: %s", readErr, label, strings.Join(names, ", "))
}
