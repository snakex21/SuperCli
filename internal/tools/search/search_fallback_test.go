package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeSearchFixture(t testing.TB, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSearchFallbackFindsSourcesWithoutExtensionWhitelist(t *testing.T) {
	dir := t.TempDir()
	names := []string{"src/catalog.zig", "build.zig.zon", "tools/start.cmd", "tools/install.ps1", "tools/setup.bat", "web/style.css", "web/index.html", "Makefile", "Dockerfile", "go.mod", "source.custom"}
	for _, name := range names {
		writeSearchFixture(t, dir, name, "first\nDetectedSystem\nlast\n")
	}
	writeSearchFixture(t, dir, "payload.bin", "\x00DetectedSystem\n")
	writeSearchFixture(t, dir, "misnamed.go", "\x00DetectedSystem\n")
	tool := NewSearchCode(dir)
	result, err := tool.fallback(context.Background(), dir, "DetectedSystem", 50)
	if err != nil || result.Err != nil {
		t.Fatalf("%v %v", err, result.Err)
	}
	lines := strings.Split(result.Text, "\n")
	if len(lines) != len(names) {
		t.Fatalf("wrong matches: %s", result.Text)
	}
	for _, name := range names {
		if !strings.Contains(result.Text, name+":2:DetectedSystem") {
			t.Fatalf("missing %s: %s", name, result.Text)
		}
	}
	if strings.Contains(result.Text, dir) || strings.Contains(result.Text, ".bin") || strings.Contains(result.Text, "misnamed") {
		t.Fatal(result.Text)
	}
}

func TestSearchFallbackCacheDoesNotCrowdOutSource(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".zig-cache/h/a.txt", "zig-cache/a.txt", "zig-out/a.txt", ".tmp/a.txt", "supercli-data/a.txt", "NODE_MODULES/a.txt"} {
		writeSearchFixture(t, dir, name, strings.Repeat("DetectedSystem cached\n", 100))
	}
	writeSearchFixture(t, dir, "src/catalog.zig", "const DetectedSystem = struct {};\n")
	result, err := NewSearchCode(dir).fallback(context.Background(), dir, "DetectedSystem", 1)
	if err != nil || result.Err != nil || result.Text != "src/catalog.zig:1:const DetectedSystem = struct {};\n"+searchLimitNotice(1) {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSearchMissingRootIsAnErrorAndCommaNameRemainsLiteral(t *testing.T) {
	dir := t.TempDir()
	tool := NewSearchCode(dir)
	for _, path := range []string{"missing", "src,tools,build.zig"} {
		result, err := tool.fallback(context.Background(), filepath.Join(dir, path), "needle", 10)
		if err != nil || result.Err == nil || !errors.Is(result.Err, os.ErrNotExist) || result.Text == "no matches" {
			t.Fatalf("%+v %v", result, err)
		}
		if strings.Contains(path, ",") && !strings.Contains(result.Err.Error(), "one existing file or directory") {
			t.Fatal(result.Err)
		}
	}
	writeSearchFixture(t, dir, "src,tools/note.txt", "needle\n")
	result, err := tool.fallback(context.Background(), filepath.Join(dir, "src,tools"), "needle", 10)
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "src,tools/note.txt:1:needle") {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSearchRelativeWorkspaceAndNarrowRoot(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "src/example.zig", "DetectedSystem\n")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewSearchCode(rel)
	for _, path := range []string{"", "src", "src/example.zig"} {
		args, _ := json.Marshal(map[string]any{"query": "DetectedSystem", "path": path, "context": 0})
		result, err := tool.run(context.Background(), args)
		if err != nil || result.Err != nil || result.Text != "src/example.zig:1:DetectedSystem" {
			t.Fatalf("path=%s: %+v %v", path, result, err)
		}
	}
}

func TestSearchCancellationAndOversizedLineCannotLookSuccessful(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "a.txt", "needle\n"+strings.Repeat("x", 1024*1024+1)+"\nneedle after\n")
	tool := NewSearchCode(dir)
	result, err := tool.fallback(context.Background(), dir, "needle", 50)
	if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "search incomplete") || !strings.Contains(result.Err.Error(), "token too long") || !strings.Contains(result.Text, "a.txt:1:needle") {
		t.Fatalf("%+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = tool.fallback(ctx, dir, "needle", 50)
	if err != nil || !errors.Is(result.Err, context.Canceled) || result.Text == "no matches" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSearchDisplayPathRoundTripsAndExternalStaysAbsolute(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	tool := NewSearchCode(home)
	internal := filepath.Join(home, "src", "a.zig")
	external := filepath.Join(root, "shared", "a.zig")
	if got := tool.displaySearchLine(internal + ":42:const value = \"x:7:y\";"); got != "src/a.zig:42:const value = \"x:7:y\";" {
		t.Fatal(got)
	}
	if got := tool.displaySearchLine(external + ":3:needle"); got != external+":3:needle" {
		t.Fatal(got)
	}
}

// Fixed all-Go, no-match corpus keeps old/new search coverage identical.
// It isolates buffer reuse and matching bytes rather than allocating strings.
func BenchmarkSearchFallbackBuffers(b *testing.B) {
	dir := b.TempDir()
	body := strings.Repeat("const unrelated_identifier = 123456789;\n", 512)
	for i := 0; i < 64; i++ {
		writeSearchFixture(b, dir, fmt.Sprintf("f%02d.go", i), body)
	}
	b.Run("previous_scanner", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			re := regexp.MustCompile("unseen_target")
			err := WalkFiles(dir, func(path string) error {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				scanner := bufio.NewScanner(f)
				scanner.Buffer(make([]byte, 64*1024), 1024*1024)
				for scanner.Scan() {
					if re.MatchString(scanner.Text()) {
						b.Fatal("unexpected match")
					}
				}
				return scanner.Err()
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("shared_buffers", func(b *testing.B) {
		tool := NewSearchCode(dir)
		b.ReportAllocs()
		for b.Loop() {
			result, err := tool.fallback(context.Background(), dir, "unseen_target", 50)
			if err != nil || result.Err != nil || result.Text != "no matches" {
				b.Fatalf("%+v %v", result, err)
			}
		}
	})
}
