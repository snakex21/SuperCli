package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/fileops"
)

func TestMissingReadOffersSmallSameExtensionDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "masterapi")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"protocol.go", "relay.go", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("private file contents\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range readSuggestionTools(root, "internal/masterapi/api.go") {
		t.Run(tc.tool.Name, func(t *testing.T) {
			result, err := tc.tool.Fn(context.Background(), tc.args)
			if err != nil {
				t.Fatal(err)
			}
			text := result.ModelContent()
			if !errors.Is(result.Err, os.ErrNotExist) {
				t.Fatalf("missing error identity: %+v", result)
			}
			if !strings.Contains(text, `Files with the same extension (same directory): "protocol.go", "relay.go"`) {
				t.Fatalf("lost existing file choices: %s", text)
			}
			if strings.Contains(text, "private file contents") || strings.Contains(text, "README.md") {
				t.Fatalf("unrequested content or extension: %s", text)
			}
		})
	}
}

func TestReadSiblingFallbackRemainsBounded(t *testing.T) {
	for _, count := range []int{0, 1, 3, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("other_%d.go", i)), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "missing.go")
			original := fileops.FileErr(os.ErrNotExist, path)
			got := suggestReadFile(context.Background(), path, original)
			wantHint := count > 0 && count <= 3
			if strings.Contains(got.Error(), "Files with the same extension") != wantHint {
				t.Fatalf("count=%d hint=%v", count, got)
			}
		})
	}
	dir := t.TempDir()
	for i := 0; i < maxSuggestionEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry_%04d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "other.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "missing.go")
	original := fileops.FileErr(os.ErrNotExist, path)
	if got := suggestReadFile(context.Background(), path, original); got != original {
		t.Fatalf("partial directory listing presented as small: %v", got)
	}
}
