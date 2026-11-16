package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/llm"
)

func compactCarryTotal(c compactUserContext) int {
	n := 0
	for _, s := range append(append([]string{}, c.Requests...), c.PriorState...) {
		n += compactCarryTokens(s)
	}
	return n
}

func TestCompactUserContextBudgetIsWindowFraction(t *testing.T) {
	for _, tc := range []struct{ window, want int }{
		{0, defaultContextWindow / 8},
		{-5, defaultContextWindow / 8},
		{2048, compactUserContextMinBudget},
		{32000, 4000},
		{200000, 25000},
		{1_000_000, compactUserContextMaxBudget},
	} {
		if got := compactUserContextBudget(tc.window); got != tc.want {
			t.Errorf("budget(%d) = %d, want %d", tc.window, got, tc.want)
		}
		// The carry must always leave room below the auto threshold.
		if tc.window >= 8192 && compactUserContextBudget(tc.window) > autoCompactThreshold(tc.window)/4 {
			t.Errorf("budget(%d) is not a small fraction of the threshold", tc.window)
		}
	}
}

func TestCompactUserExcerptKeepsHeadTailAndMarker(t *testing.T) {
	short := "Never push. Keep the reasoning level."
	if got := compactUserExcerpt(short, 100); got != short {
		t.Fatalf("short request changed: %q", got)
	}
	long := "HEAD-CONSTRAINT: no publishing.\n" + strings.Repeat("日本語 log line <tag> & \"quoted\" 😀\n", 400) + "TAIL-CONSTRAINT: answer in Polish."
	for _, limit := range []int{40, 100, 333, 1000} {
		got := compactUserExcerpt(long, limit)
		if tok := compactCarryTokens(got); tok > limit {
			t.Fatalf("limit %d: excerpt is %d tokens", limit, tok)
		}
		if !strings.Contains(got, "omitted to fit the compaction budget") {
			t.Fatalf("limit %d: no elision marker", limit)
		}
		if limit >= 100 && (!strings.HasPrefix(got, "HEAD-CONSTRAINT: no publishing.") || !strings.HasSuffix(got, "TAIL-CONSTRAINT: answer in Polish.")) {
			t.Fatalf("limit %d: head or tail constraint lost: %q", limit, got)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d: excerpt split a rune", limit)
		}
		if again := compactUserExcerpt(got, limit); again != got {
			t.Fatalf("limit %d: excerpt is not idempotent", limit)
		}
	}
}

func TestBoundCompactUserContextKeepsNewestVerbatimAndFitsBudget(t *testing.T) {
	const budget = 4000
	paste := strings.Repeat("2026-11-16T10:00:00Z worker[42] WARN cache miss key=user/profile/0042 latency=118ms\n", 110)
	var reqs []string
	for i := 1; i <= 30; i++ {
		reqs = append(reqs, fmt.Sprintf("REQ-%02d-START please handle this.\n%sREQ-%02d-END constraint", i, paste, i))
	}
	reqs[len(reqs)-1] = "newest: keep this exact text, do not publish."
	got := boundCompactUserContext(compactUserContext{Requests: reqs}, budget)
	if total := compactCarryTotal(got); total > budget {
		t.Fatalf("carry %d tokens exceeds budget %d", total, budget)
	}
	if got.Requests[len(got.Requests)-1] != reqs[len(reqs)-1] {
		t.Fatal("newest request was not kept verbatim")
	}
	first := got.Requests[0]
	if !strings.HasPrefix(first, "REQ-01-START") || !strings.HasSuffix(first, "REQ-01-END constraint") {
		t.Fatalf("original task lost its head or tail: %.120q", first)
	}
	n, ok := compactOmittedRequestsCount(got.Requests[1])
	if !ok || n == 0 {
		t.Fatalf("no counted omission marker after the original task: %q", got.Requests[1])
	}
	if kept := len(got.Requests) - 1; n+kept != len(reqs) {
		t.Fatalf("omitted %d + kept %d != %d requests", n, kept, len(reqs))
	}
	// Re-bounding (the next compaction imports this carry) changes nothing.
	if again := boundCompactUserContext(got, budget); !reflect.DeepEqual(again, got) {
		t.Fatal("bounding is not idempotent")
	}
	// A further request folds the omission count instead of stacking markers.
	next := boundCompactUserContext(compactUserContext{Requests: append(append([]string{}, got.Requests...), reqs[5], "newer")}, budget)
	markers, total := 0, 0
	for _, r := range next.Requests {
		if c, ok := compactOmittedRequestsCount(r); ok {
			markers++
			total += c
		} else {
			total++
		}
	}
	if markers != 1 || total != len(reqs)+2 {
		t.Fatalf("markers=%d accounted=%d, want 1 marker accounting %d requests", markers, total, len(reqs)+2)
	}
}

func TestBoundCompactUserContextTrimsOversizedNewest(t *testing.T) {
	newest := "LATEST-HEAD keep reasoning level.\n" + strings.Repeat("pasted spec line\n", 2000) + "LATEST-TAIL no publishing."
	got := boundCompactUserContext(compactUserContext{Requests: []string{"old short", newest}}, 600)
	if total := compactCarryTotal(got); total > 600 {
		t.Fatalf("carry %d exceeds budget", total)
	}
	last := got.Requests[len(got.Requests)-1]
	if !strings.HasPrefix(last, "LATEST-HEAD") || !strings.HasSuffix(last, "LATEST-TAIL no publishing.") || !strings.Contains(last, "omitted to fit the compaction budget") {
		t.Fatalf("oversized newest request not trimmed with marker: %.120q", last)
	}
	if got.Requests[0] != "old short" {
		t.Fatal("short older request changed")
	}
}

func TestBoundCompactUserContextSmallCarryUnchanged(t *testing.T) {
	in := compactUserContext{Requests: []string{"a", "Never push.", "Keep 日本語."}, PriorState: []string{"State: offline only."}}
	if got := boundCompactUserContext(in, compactUserContextBudget(0)); !reflect.DeepEqual(got, in) {
		t.Fatalf("small carry changed: %+v", got)
	}
}

func TestBoundCompactUserContextBoundsLegacyPriorState(t *testing.T) {
	huge := "State: legacy." + strings.Repeat(" old state", 5000)
	got := boundCompactUserContext(compactUserContext{Requests: []string{"req"}, PriorState: []string{huge}}, 1000)
	if tok := compactCarryTokens(got.PriorState[0]); tok > 1000/compactUserContextPriorDivisor {
		t.Fatalf("prior state %d tokens exceeds its share", tok)
	}
	if got.Requests[0] != "req" {
		t.Fatal("request changed")
	}
}

// The summarizer bounds the carry by the window the loop puts in its context.
func TestAutoSummarizerBoundsCarryByContextWindow(t *testing.T) {
	big := strings.Repeat("evidence line\n", 3000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "OLD-HEAD " + big + " OLD-TAIL"},
		{Role: llm.RoleAssistant, Content: "done"},
		{Role: llm.RoleUser, Content: "second request"},
	}
	for _, window := range []int{8192, 128000} {
		p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "Goal: continue.", FinishReason: "stop"}}}}
		ctx := withCompactWindow(llm.WithPurpose(context.Background(), llm.PurposeCompact), window)
		out, err := NewAutoSummarizer(nil)(ctx, p, msgs)
		if err != nil {
			t.Fatal(err)
		}
		carry := decodeCompactUserContext(t, out)
		if total := compactCarryTotal(carry); total > compactUserContextBudget(window) {
			t.Fatalf("window %d: carry %d exceeds budget %d", window, total, compactUserContextBudget(window))
		}
		if carry.Requests[1] != "second request" || !strings.HasPrefix(carry.Requests[0], "OLD-HEAD") || !strings.HasSuffix(carry.Requests[0], "OLD-TAIL") {
			t.Fatalf("window %d: unexpected carry %+v", window, carry.Requests)
		}
	}
}
