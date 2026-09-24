package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"supercli/internal/tools/fileops"
)

// findFiles deliberately uses the native walker regardless of rg availability.
// Like list_dir it enumerates regular files (including empty/binary files), uses
// the shared build/dependency exclusions, and never opens file contents.
// A filename lookup should not need a subprocess, regex scan or a cached index.
func (s *SearchCode) findFiles(ctx context.Context, root string, include *searchGlob, max int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, nil
	}
	if _, err := os.Stat(root); err != nil {
		return Result{Err: fmt.Errorf("search_code: %w", fileops.FileErr(err, root))}, nil
	}
	var b strings.Builder
	count := 0
	err := walkFilesFiltered(ctx, root,
		func(dir string) bool { return include.canDescend(root, dir) },
		func(file string) error {
			if !include.matches(root, file) {
				return nil
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s.displayPath(file))
			count++
			if count >= max {
				return errStopWalk
			}
			return nil
		})
	if err != nil && !errors.Is(err, errStopWalk) {
		return Result{Text: b.String(), Err: fmt.Errorf("file search incomplete: %w", err)}, nil
	}
	if count == 0 {
		return Result{Text: "no matches"}, nil
	}
	if count >= max {
		fmt.Fprintf(&b, "\n[file limit reached: %d paths; results may be incomplete. Narrow path/include or raise max.]", max)
	}
	return Result{Text: b.String()}, nil
}
