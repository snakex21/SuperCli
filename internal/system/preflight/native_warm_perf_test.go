package preflight

import (
	"strings"
	"testing"
)

// This deliberately preserves the existing two-second log-cache policy. It
// measures repeated real builds separately from uncached subprocess fixtures.
func BenchmarkPreflightNativeWarm(b *testing.B) {
	root := preflightNativeFixture(b, 6)
	block := Build(root, Options{})
	if !strings.Contains(block, "fixture newest") {
		b.Fatal("invalid immutable fixture")
	}
	b.ReportAllocs()
	for b.Loop() {
		preflightBriefingSink = Build(root, Options{})
		if preflightBriefingSink != block {
			b.Fatal("immutable fixture changed")
		}
	}
}
