package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSearchPreviewPayload(b *testing.B) {
	for _, size := range []int{40, 12000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			dir := b.TempDir()
			b.Setenv("PATH", dir)
			for i := 0; i < 10; i++ {
				writeSearchFixture(b, dir, fmt.Sprintf("%02d.txt", i), strings.Repeat("x", size/2)+" needle=123 "+strings.Repeat("z", size/2)+"\n")
			}
			spec := NewSearchCode(dir).Spec()
			args := json.RawMessage(`{"query":"needle","context":0}`)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := spec.Fn(context.Background(), args)
				if err != nil || result.Err != nil {
					b.Fatalf("%v %v", err, result.Err)
				}
			}
		})
	}
}
