package fileops

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPatchWrittenSnapshotAtEOF(t *testing.T) {
	for _, suffix := range []string{"", "\n", "\r\n"} {
		after := strings.Repeat("prefix\n", 40) + "last remaining" + suffix
		preview := patchWrittenSnapshot(after+"deleted", after)
		if !strings.Contains(preview, "  41 | last remaining") || !strings.Contains(preview, "Partial snapshot") {
			t.Fatalf("bad EOF context: %q", preview)
		}
	}
}

func TestPatchWrittenSnapshotLineAndByteLimits(t *testing.T) {
	for _, count := range []int{1, 16, 17, 200} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			after := strings.Repeat("źródło\n", count)
			text := patchWrittenSnapshot("old\n", after)
			if !utf8.ValidString(text) || len(text) > patchSnapshotBytes {
				t.Fatalf("invalid bounded snapshot: %q", text)
			}
			if strings.Count(text, " | ") > patchSnapshotLines {
				t.Fatalf("too many displayed lines: %q", text)
			}
			if (count > 16) != strings.Contains(text, "Partial snapshot") {
				t.Fatalf("wrong completeness marker: %q", text)
			}
		})
	}
}

func TestPatchWrittenSnapshotShowsFinalStateAndMarksOmissions(t *testing.T) {
	before := "first=1\n" + strings.Repeat("unchanged\n", 100) + "last=1\n"
	after := strings.ReplaceAll(before, "=1", "=2")
	text := patchWrittenSnapshot(before, after)
	if !strings.Contains(text, "1 | first=2") || !strings.Contains(text, "Partial snapshot") || strings.Contains(text, "last=1") {
		t.Fatalf("misleading partial result: %q", text)
	}
}

var patchSnapshotBenchResult string

func BenchmarkPatchWrittenSnapshot(b *testing.B) {
	for _, size := range []int{300, 100 * 1024, 1024 * 1024} {
		before := strings.Repeat("const Value = 1\n", size/16) + "const Last = 1\n"
		after := strings.TrimSuffix(before, "Last = 1\n") + "Last = 2\n"
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				patchSnapshotBenchResult = patchWrittenSnapshot(before, after)
			}
		})
	}
}
