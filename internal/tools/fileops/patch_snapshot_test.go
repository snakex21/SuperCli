package fileops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func BenchmarkPatchSnapshotRegions(b *testing.B) {
	for _, size := range []int{300, 1024 * 1024} {
		before := strings.Repeat("const Value = 1\n", size/16)
		for _, location := range []string{"first", "middle", "last", "separated"} {
			after := before
			switch location {
			case "first":
				after = "const Value = 2\n" + before[len("const Value = 1\n"):]
			case "middle":
				pos := strings.LastIndexByte(before[:len(before)/2], '\n') + 1
				after = before[:pos] + "const Value = 2\n" + before[pos+len("const Value = 1\n"):]
			case "last":
				after = before[:len(before)-len("const Value = 1\n")] + "const Value = 2\n"
			case "separated":
				after = "const Value = 2\n" + before[len("const Value = 1\n"):len(before)-len("const Value = 1\n")] + "const Value = 3\n"
			}
			b.Run(fmt.Sprintf("%d/%s", size, location), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					patchSnapshotBenchResult = patchWrittenSnapshot(before, after)
				}
			})
		}
	}
}

func TestPatchWrittenSnapshotSeparatedFinalRegions(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		before := "first=1" + ending + strings.Repeat("unchanged"+ending, 100) + "last=1" + ending
		after := strings.ReplaceAll(before, "=1", "=2")
		text := patchWrittenSnapshot(before, after)
		for _, want := range []string{"1 | first=2", "102 | last=2", "middle content omitted", "Partial snapshot"} {
			if !strings.Contains(text, want) {
				t.Fatalf("lost %q in %q", want, text)
			}
		}
		if strings.Contains(text, "\r") {
			t.Fatalf("CRLF leaked into numbered rows: %q", text)
		}
		assertPatchSnapshotBounded(t, text)
		assertPatchSnapshotRows(t, after, text)
	}
}

func TestPatchWrittenSnapshotShiftedFinalLineNumbers(t *testing.T) {
	middle := strings.Repeat("unchanged\n", 100)
	tests := []struct {
		name, before, after, first, last string
	}{
		{"insert", "first=1\n" + middle + "last=1\n",
			"first=2\ninserted-a\ninserted-b\n" + middle + "last=2\n",
			"1 | first=2", "104 | last=2"},
		{"delete", "first=1\nremove-a\nremove-b\n" + middle + "last=1\n",
			"first=2\n" + middle + "last=2\n",
			"1 | first=2", "102 | last=2"},
		{"delete-eof", "first=1\n" + middle + "remaining\nremoved-at-eof\n",
			"first=2\n" + middle + "remaining\n",
			"1 | first=2", "102 | remaining"},
		{"append-many", strings.Repeat("prefix\n", 30),
			strings.Repeat("prefix\n", 30) + "insert-first\n" + middle + "insert-last\n",
			"31 | insert-first", "132 | insert-last"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := patchWrittenSnapshot(tt.before, tt.after)
			for _, want := range []string{tt.first, tt.last, "middle content omitted", "Partial snapshot"} {
				if !strings.Contains(text, want) {
					t.Fatalf("lost %q in %q", want, text)
				}
			}
			assertPatchSnapshotBounded(t, text)
			assertPatchSnapshotRows(t, tt.after, text)
		})
	}
}

func TestPatchWrittenSnapshotChainedEditsUsesWrittenFinalState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chained.txt")
	before := "first=1\n" + strings.Repeat("unchanged\n", 100) + "last=1\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := PatchFileContext(context.Background(), path, []PatchChange{
		{Old: "first=1", New: "first=2"},
		{Old: "first=2", New: "first=3\ninserted\n"},
		{Old: "last=1", New: "last=4"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := result.WrittenPreview
	for _, want := range []string{"1 | first=3", "104 | last=4", "middle content omitted"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost final state %q: %q", want, text)
		}
	}
	if strings.Contains(text, "first=2") || strings.Contains(text, "last=1") {
		t.Fatalf("intermediate state leaked: %q", text)
	}
	assertPatchSnapshotBounded(t, text)
	assertPatchSnapshotRows(t, string(written), text)
}

func TestPatchWrittenSnapshotTwoRegionsShareByteBudget(t *testing.T) {
	before := "first=1 " + strings.Repeat("界🙂", 800) + "\n" +
		strings.Repeat("middle\n", 100) +
		"last=1 " + strings.Repeat("ź", 1200) + "\n"
	after := strings.ReplaceAll(before, "=1", "=2")
	text := patchWrittenSnapshot(before, after)
	for _, want := range []string{"1 | first=2", "102 | last=2", "line truncated", "middle content omitted"} {
		if !strings.Contains(text, want) {
			t.Fatalf("one long region starved %q: %q", want, text)
		}
	}
	assertPatchSnapshotBounded(t, text)
}

func TestPatchWrittenSnapshotOneLongLineRemainsOneRegion(t *testing.T) {
	before := "first=1 " + strings.Repeat("界", 3000) + " last=1\n"
	after := strings.ReplaceAll(before, "=1", "=2")
	text := patchWrittenSnapshot(before, after)
	if strings.Contains(text, "middle content omitted") || strings.Contains(text, "first and last") {
		t.Fatalf("one line split into regions: %q", text)
	}
	assertPatchSnapshotBounded(t, text)
}

func TestPatchWrittenSnapshotLongUnchangedSuffixUsesFirstRegion(t *testing.T) {
	before := "first=1\n" + strings.Repeat("unchanged\n", 100) + "last=1\n" +
		strings.Repeat("suffix\n", 1000)
	after := strings.ReplaceAll(before, "=1", "=2")
	text := patchWrittenSnapshot(before, after)
	if strings.Contains(text, "middle content omitted") || !strings.Contains(text, "first=2") ||
		!strings.Contains(text, "Partial snapshot") {
		t.Fatalf("bounded suffix must fall back clearly: %q", text)
	}
	assertPatchSnapshotBounded(t, text)
}

func TestPatchSnapshotBoundaryComparison(t *testing.T) {
	for _, size := range []int{0, 1, 255, 256, 257, 4095, 4096, 4097, 8192} {
		for _, position := range []int{0, size / 2, size} {
			before := strings.Repeat("a", size)
			after := before[:position] + "b" + before[position:]
			first := patchSnapshotPrefix(before, after)
			if first != position {
				t.Fatalf("prefix size=%d position=%d got=%d", size, position, first)
			}
			last, found := patchSnapshotLastDifference(before, after, first)
			if size-position > 4096 {
				if found {
					t.Fatalf("scanned beyond bounded suffix size=%d position=%d", size, position)
				}
			} else if !found || last != position {
				t.Fatalf("last insertion size=%d position=%d got=%d/%v", size, position, last, found)
			}
		}
	}
}

func assertPatchSnapshotBounded(t *testing.T, text string) {
	t.Helper()
	if !utf8.ValidString(text) || len(text) > patchSnapshotBytes ||
		strings.Count(text, " | ") > patchSnapshotLines {
		t.Fatalf("snapshot exceeds its fixed budget: bytes=%d %q", len(text), text)
	}
}

func assertPatchSnapshotRows(t *testing.T, after, text string) {
	t.Helper()
	lines := strings.Split(after, "\n")
	for _, row := range strings.Split(text, "\n") {
		number, body, ok := strings.Cut(row, " | ")
		if !ok || strings.Contains(body, "[line truncated]") {
			continue
		}
		line, err := strconv.Atoi(strings.TrimSpace(number))
		if err != nil || line < 1 || line > len(lines) ||
			body != strings.TrimSuffix(lines[line-1], "\r") {
			t.Fatalf("row does not match final file: %q", row)
		}
	}
}

func TestPatchSnapshotBoundariesMatchSimpleComparison(t *testing.T) {
	for _, size := range []int{0, 7, 255, 256, 257, 1024, 4095, 4096, 4097, 9000} {
		base := strings.Repeat("abcdefgh", size/8) + strings.Repeat("z", size%8)
		for _, position := range []int{0, size / 2, size} {
			for _, kind := range []string{"insert", "replace", "delete"} {
				after := base
				switch kind {
				case "insert":
					after = base[:position] + "ź\nnew\n" + base[position:]
				case "replace":
					if position < size {
						after = base[:position] + "x" + base[position+1:]
					}
				case "delete":
					if position < size {
						after = base[:position] + base[min(size, position+3):]
					}
				}
				first := 0
				for first < len(base) && first < len(after) && base[first] == after[first] {
					first++
				}
				if got := patchSnapshotPrefix(base, after); got != first {
					t.Fatalf("prefix %s/%d/%d: got=%d want=%d", kind, size, position, got, first)
				}
				beforeEnd, afterEnd := len(base)-1, len(after)-1
				suffix := 0
				for beforeEnd >= first && afterEnd >= first && base[beforeEnd] == after[afterEnd] {
					beforeEnd--
					afterEnd--
					suffix++
				}
				last, found := patchSnapshotLastDifference(base, after, first)
				expected := afterEnd >= first && suffix < 4096
				// The complete shorter remainder may fit exactly in the cap.
				if suffix == 4096 && min(len(base)-first, len(after)-first) == 4096 {
					expected = afterEnd >= first
				}
				if found != expected || (found && last != afterEnd) {
					t.Fatalf("last %s/%d/%d: got=%d/%v want=%d/%v suffix=%d",
						kind, size, position, last, found, afterEnd, expected, suffix)
				}
			}
		}
	}
}

func TestPatchWrittenSnapshotLongContextDoesNotHideChangedRows(t *testing.T) {
	longContext := strings.Repeat("unchanged-context ", 120) + "\n"
	before := longContext + longContext + "first=1\n" + strings.Repeat("middle\n", 100) + longContext + longContext + "last=1\n"
	after := strings.ReplaceAll(before, "=1", "=2")
	text := patchWrittenSnapshot(before, after)
	for _, want := range []string{"3 | first=2", "106 | last=2", "middle content omitted"} {
		if !strings.Contains(text, want) {
			t.Fatalf("long context hid %q: %q", want, text)
		}
	}
	assertPatchSnapshotBounded(t, text)
	assertPatchSnapshotRows(t, after, text)
}
