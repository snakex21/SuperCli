package core

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutputSearchKeepsCompleteMatchedLineWithinExistingBudget(t *testing.T) {
	s := NewOutputStore()
	target := "RetryPolicy " + strings.Repeat("label ", 38) + "effective_limit=6842\n"
	source := strings.Repeat("routine log detail\n", 1000) + target + strings.Repeat("routine log detail\n", 1000)
	s.Compact("ctx_execute", source)
	found := searchOutput(t, s, "RetryPolicy", 0, 0)
	if !strings.Contains(found, target) {
		t.Fatalf("matched line cut: %s", found)
	}
	if len(found) > 600 {
		t.Fatalf("oversized evidence: %d", len(found))
	}
}

func TestOutputSearchLineExcerptsPreserveOffsetsBudgetAndPagination(t *testing.T) {
	for _, limit := range []int{25, 100, 400, 4096} {
		for _, ending := range []string{"\n", "\r\n"} {
			s := NewOutputStore()
			var src strings.Builder
			for i := 0; i < 20; i++ {
				fmt.Fprintf(&src, "%s%skey-%02d %s answer_%02d%s", strings.Repeat("padding", 55), ending, i, strings.Repeat("ż", 60), i, ending)
			}
			text := src.String()
			s.put(text)
			header := regexp.MustCompile("\\[bytes ([0-9]+):([0-9]+)\\]\\n")
			nextRe := regexp.MustCompile("next search offset: ([0-9]+)")
			seen := make(map[int]bool)
			offset := 0
			for page := 0; page < 100; page++ {
				got := searchOutput(t, s, "key-", offset, limit)
				if !utf8.ValidString(got) {
					t.Fatalf("invalid utf8: %q", got)
				}
				used := 0
				for _, idx := range header.FindAllStringSubmatchIndex(got, -1) {
					start, _ := strconv.Atoi(got[idx[2]:idx[3]])
					end, _ := strconv.Atoi(got[idx[4]:idx[5]])
					if start < 0 || end > len(text) || end <= start || !strings.HasPrefix(got[idx[1]:], text[start:end]) {
						t.Fatalf("wrong byte coordinates: %s", got)
					}
					used += end - start
					for i := 0; i < 20; i++ {
						matchAt := strings.Index(text, fmt.Sprintf("key-%02d", i))
						if start <= matchAt && matchAt+len("key-") <= end {
							seen[i] = true
						}
					}
				}
				if used > limit {
					t.Fatalf("budget exceeded: %d > %d", used, limit)
				}
				next := nextRe.FindStringSubmatch(got)
				if next == nil {
					break
				}
				n, _ := strconv.Atoi(next[1])
				if n <= offset {
					t.Fatal("pagination did not advance")
				}
				offset = n
				if page == 99 {
					t.Fatal("pagination did not finish")
				}
			}
			if len(seen) != 20 {
				t.Fatalf("limit=%d ending=%q: matches=%d", limit, ending, len(seen))
			}
		}
	}
}

func BenchmarkOutputSearchLineExcerpts(b *testing.B) {
	for _, name := range []string{"long-hit-line", "short-lines", "minified"} {
		text := strings.Repeat("routine detail\n", 1000) + "RetryPolicy " + strings.Repeat("label ", 38) + "effective_limit=6842\n" + strings.Repeat("routine detail\n", 1000)
		if name == "short-lines" {
			text = strings.Repeat("routine detail\n", 1000) + "RetryPolicy=6842\n" + strings.Repeat("routine detail\n", 1000)
		}
		if name == "minified" {
			text = strings.Repeat("z", 20000) + "RetryPolicy=6842" + strings.Repeat("z", 20000)
		}
		s := NewOutputStore()
		handle, _ := s.put(text)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := s.search(context.Background(), readOutputArgs{Handle: handle, Query: "RetryPolicy"})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestOutputSearchLineWindowControls(t *testing.T) {
	for _, tc := range []struct {
		name, text, query string
		limit             int
	}{
		{"minified", strings.Repeat("x", 1000) + "TARGET" + strings.Repeat("y", 1000), "TARGET", 400},
		{"multiline", "before\nalpha\nbeta\nafter", "alpha\nbeta", 40},
		{"unicode", strings.Repeat("ż", 80) + "\nTARGET " + strings.Repeat("ą", 80) + " wartość=42\nnext", "TARGET", 400},
		{"no-final-newline", strings.Repeat("old line\n", 50) + "TARGET " + strings.Repeat("label ", 30) + "value=42", "TARGET", 400},
		{"query-is-line-end", "before\nTARGET\nafter", "TARGET\n", 400},
		{"query-only-budget", "prefix TARGET suffix", "TARGET", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewOutputStore()
			s.put(tc.text)
			got := searchOutput(t, s, tc.query, 0, tc.limit)
			if !strings.Contains(got, tc.query) || !utf8.ValidString(got) {
				t.Fatalf("lost query: %q", got)
			}
			if tc.name == "no-final-newline" && !strings.Contains(got, "value=42") {
				t.Fatal("last line cut")
			}
			if tc.name == "minified" {
				pos := strings.Index(tc.text, tc.query)
				want := tc.text[pos-outputSearchContext : pos+len(tc.query)+outputSearchContext]
				if !strings.Contains(got, want) {
					t.Fatal("minified fallback changed")
				}
			}
		})
	}
}
