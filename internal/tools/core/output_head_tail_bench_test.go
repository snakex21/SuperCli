package core

import (
	"fmt"
	"strings"
	"testing"
)

var headTailBenchmarkSink string

// Measure truncation after capture: its omitted middle must not be copied
// merely to count newlines. Input creation is outside the timed loop.
func BenchmarkHeadTailLargeOutput(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 16 << 20} {
		text := strings.Repeat("abcdefghijklmno\n", size/16)
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for i := 0; i < b.N; i++ {
				headTailBenchmarkSink = HeadTail(text, outputPreviewHead, outputPreviewTail)
			}
		})
	}
}
