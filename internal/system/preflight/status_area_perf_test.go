package preflight

import (
	"math/rand"
	"strings"
	"testing"
)

// Independent pre-optimization parser preserves handling of opaque Git paths,
// quotes, Windows separators and rename destinations.
func originalStatusArea(path string) string {
	path = strings.Trim(path, `"`)
	path = strings.ReplaceAll(path, `\`, "/")
	if arrow := strings.LastIndex(path, " -> "); arrow >= 0 {
		path = strings.TrimSpace(path[arrow+4:])
	}
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] == "internal" {
		return strings.Join(parts[:2], "/")
	}
	if len(parts) >= 2 && (parts[0] == "cmd" || parts[0] == "docs" || parts[0] == "test") {
		return parts[0]
	}
	if len(parts) > 1 {
		return parts[0]
	}
	return "root"
}

func TestStatusAreaPreservesOpaquePathGrouping(t *testing.T) {
	paths := []string{"", "root.go", "internal/service/handler.go", "internal/handler.go", "internal//handler.go", "internal/", "/root.go", "cmd/tool/main.go", "docs/API.md", "test/node.test.cjs", `"internal\service\źródło.go"`, `old.go -> internal/service/new.go`, `old -> path -> docs/new.md`, "\x00\xff/ę世", "dir name/file.go"}
	rng := rand.New(rand.NewSource(90217))
	atoms := []string{"", "internal", "cmd", "docs", "test", "nested", "źródło", "\x00\xff", " ", `"`, " -> ", "/", `\`}
	for n := 0; n < 2000; n++ {
		var b strings.Builder
		for p := 0; p < rng.Intn(12); p++ {
			b.WriteString(atoms[rng.Intn(len(atoms))])
		}
		paths = append(paths, b.String())
	}
	for _, path := range paths {
		if got, want := statusArea(path), originalStatusArea(path); got != want {
			t.Fatalf("path %q: area %q, want %q", path, got, want)
		}
	}
}

var statusAreaPerfSink string

func BenchmarkStatusAreaGrouping(b *testing.B) {
	path := "internal/service/nested/handler.go"
	for _, implementation := range []struct {
		name string
		fn   func(string) string
	}{{"original", originalStatusArea}, {"production", statusArea}} {
		b.Run(implementation.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				statusAreaPerfSink = implementation.fn(path)
			}
		})
	}
}
