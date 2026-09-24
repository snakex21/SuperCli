package preflight

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Measures optional startup context collection, not model inference.
func BenchmarkStartupFallback(b *testing.B) {
	root := b.TempDir()
	for d := 0; d < 120; d++ {
		dir := filepath.Join(root, fmt.Sprintf("area-%03d", d))
		if err := os.Mkdir(dir, 0755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < 100; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("source-%03d.go", f)), []byte("package fixture"), 0644); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := Build(root, Options{LookPath: noGit}); got == "" {
			b.Fatal("empty briefing")
		}
	}
}
