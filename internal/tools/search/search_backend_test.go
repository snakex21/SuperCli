package search

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Opt-in comparison includes ripgrep process startup, traversal and rendering.
// Fixtures contain only searchable files, so both engines examine the same data.
func BenchmarkSearchBackend(b *testing.B) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		b.Skip("set SUPERCLI_TEST_RG to compare real ripgrep")
	}
	for _, size := range []struct {
		name         string
		files, lines int
	}{{"small", 8, 128}, {"large", 512, 2048}} {
		b.Run(size.name, func(b *testing.B) {
			dir := b.TempDir()
			body := strings.Repeat("const unrelated_identifier = 123456789;\n", size.lines)
			for i := 0; i < size.files; i++ {
				writeSearchFixture(b, dir, fmt.Sprintf("src/file%04d.go", i), body)
			}
			tool := NewSearchCode(dir)
			for _, backend := range []string{"go", "rg"} {
				b.Run(backend, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						var result Result
						var err error
						if backend == "go" {
							result, err = tool.fallback(context.Background(), dir, "unseen_target", 50)
						} else {
							result, err = tool.ripgrep(context.Background(), rg, dir, "unseen_target", 50)
						}
						if err != nil || result.Err != nil || result.Text != "no matches" {
							b.Fatalf("%+v %v", result, err)
						}
					}
				})
			}
		})
	}
}
