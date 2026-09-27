package files

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkReadManyFileGrouping(b *testing.B) {
	for _, tc := range []struct {
		name         string
		sameFile     bool
		first, lines int
	}{
		{"same_near", true, 1, 1600},
		{"same_late", true, 40001, 42000},
		{"different_small", false, 1, 300},
		{"single_small", true, 1, 300},
	} {
		b.Run(tc.name, func(b *testing.B) {
			root := b.TempDir()
			body := strings.Repeat("fixed source content for a numbered line\n", tc.lines)
			var reads []string
			count := 4
			if tc.name == "single_small" {
				count = 1
			}
			for i := 0; i < count; i++ {
				name := "source-0.go"
				from := tc.first
				if tc.sameFile {
					from += i * 200
				} else {
					name = fmt.Sprintf("source-%d.go", i)
				}
				if i == 0 || !tc.sameFile {
					if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
						b.Fatal(err)
					}
				}
				reads = append(reads, fmt.Sprintf("%s:%d-%d", name, from, from+299))
			}
			raw, _ := json.Marshal(map[string]string{"reads": strings.Join(reads, " | ")})
			tool := NewReadMany(root)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := tool.execute(context.Background(), raw)
				if err != nil || result.Err != nil || strings.Contains(result.Text, "error:") {
					b.Fatalf("%+v %v", result, err)
				}
			}
		})
	}
}
