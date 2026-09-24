package files

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

func readSuggestionTools(base, path string) []struct {
	tool Tool
	args json.RawMessage
} {
	lineArgs, _ := json.Marshal(readLinesArgs{File: path, From: 1, To: 1})
	contextArgs, _ := json.Marshal(readContextArgs{File: path, Line: 1, Radius: 1})
	manyArgs, _ := json.Marshal(map[string]string{"reads": path + ":1-1"})
	return []struct {
		tool Tool
		args json.RawMessage
	}{
		{NewReadLines(base).Spec(), lineArgs}, {NewReadContext(base).Spec(), contextArgs}, {NewReadMany(base).Spec(), manyArgs},
	}
}

func TestMissingReadSuggestsExistingSiblingWithoutReadingIt(t *testing.T) {
	dir := t.TempDir()
	candidate := "win7_uefi_files.go"
	if err := os.WriteFile(filepath.Join(dir, candidate), []byte("sibling content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range readSuggestionTools(dir, "win7_uefi_files_test.go") {
		t.Run(tc.tool.Name, func(t *testing.T) {
			result, err := tc.tool.Fn(context.Background(), tc.args)
			if err != nil {
				t.Fatal(err)
			}
			text := result.ModelContent()
			if !strings.Contains(text, "not_found") || !strings.Contains(text, "Similar files (same directory): "+strconv.Quote(candidate)) {
				t.Fatalf("missing recovery context: %s", text)
			}
			if tc.tool.Name == "read_many" {
				if !strings.Contains(text, "0 ok, 1 failed") {
					t.Fatal("missing read became success")
				}
			} else if !errors.Is(result.Err, os.ErrNotExist) {
				t.Fatal("missing read lost error identity")
			}
			if strings.Contains(text, "sibling content") {
				t.Fatal("silently read another file")
			}
		})
	}
	// The model can choose the suggestion directly, without listing the directory.
	for _, tc := range readSuggestionTools(dir, candidate) {
		result, err := tc.tool.Fn(context.Background(), tc.args)
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "sibling content") || strings.Contains(result.Text, "Similar files") {
			t.Fatalf("corrected read: %+v, %v", result, err)
		}
	}
}

func TestReadSuggestionsAreShortLocalAndBestEffort(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"source_a.go", "source_b.go", "source_c.go", "source_d.go", "source.txt", "unrelated.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "source_dir.go"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "source.go")
	original := fileops.FileErr(os.ErrNotExist, path)
	got := suggestReadFile(context.Background(), path, original)
	want := "Similar files (same directory): \"source_a.go\", \"source_b.go\", \"source_c.go\""
	if !strings.HasSuffix(got.Error(), want) || !errors.Is(got, os.ErrNotExist) {
		t.Fatalf("suggestions: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if suggestReadFile(ctx, path, original) != original {
		t.Fatal("cancelled suggestion did more work")
	}
	for _, other := range []error{nil, os.ErrPermission, errors.New("invalid range"), errors.New("not_found " + path)} {
		if suggestReadFile(context.Background(), path, other) != other {
			t.Fatal("suggested on another error class")
		}
	}
	for _, missing := range []string{"unknown.go", "source.json", "missing/source.go"} {
		full := filepath.Join(dir, missing)
		err := fileops.FileErr(os.ErrNotExist, full)
		if suggestReadFile(context.Background(), full, err) != err {
			t.Fatalf("irrelevant or recursive suggestion for %s", missing)
		}
	}
}

func TestReadSuggestionsRespectSandbox(t *testing.T) {
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range readSuggestionTools(workspace, filepath.Join(dir, "outside_test.go")) {
		result, err := tc.tool.Fn(context.Background(), tc.args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.ModelContent(), "Similar files") {
			t.Fatal("suggestions escaped workspace")
		}
		if tc.tool.Name == "read_many" {
			if !strings.Contains(result.Text, "0 ok, 1 failed") {
				t.Fatal("escape not rejected")
			}
		} else if result.Err == nil {
			t.Fatal("escape not rejected")
		}
	}
}

func TestReadSuggestionsSkipSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target.go")
	if err := os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "source_link.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	path := filepath.Join(dir, "source.go")
	original := fileops.FileErr(os.ErrNotExist, path)
	if suggestReadFile(context.Background(), path, original) != original {
		t.Fatal("suggested symlink")
	}
}
