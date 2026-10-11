package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
)

func compactionByteLimitText(size int) string {
	const head = "Goal: continue work\nDone: verified source\n"
	const tail = "\nState: Preserve all earlier constraints.\nPending: Continue unresolved work."
	return head + strings.Repeat("x", size-len(head)-len(tail)) + tail
}

func TestSummarizeForCompactionCompletedByteLimit(t *testing.T) {
	exact := compactionByteLimitText(compactSummaryMaxChars)
	oversized := compactionByteLimitText(compactSummaryMaxChars + 1)
	lateState := "Goal: continue\nDone: " + strings.Repeat("x", compactSummaryMaxChars) + "\nState: Preserve earlier restrictions.\nPending: Complete unresolved work."
	cases := []struct {
		name    string
		wire    string
		want    string
		tooLong bool
	}{
		{"exact-4000", exact, exact, false},
		{"4001-with-pending-at-tail", oversized, "", true},
		{"state-and-pending-after-cap", lateState, "", true},
		{"utf8-exact-4000", strings.Repeat("x", compactSummaryMaxChars-2) + "ż", strings.Repeat("x", compactSummaryMaxChars-2) + "ż", false},
		{"utf8-crosses-4000", strings.Repeat("x", compactSummaryMaxChars-1) + "ż", "", true},
		{"thinking-and-outer-space-before-size-check", " \n<thinking>" + strings.Repeat("reasoning ", 2000) + "</thinking>\n" + exact + " \t\n", exact, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !utf8.ValidString(tc.wire) {
				t.Fatal("fixture is not valid UTF-8")
			}
			p := &stubProvider{name: "summary", scripts: [][]llm.Delta{{{Content: tc.wire}, {FinishReason: "stop"}}}}
			messages := []llm.Message{{Role: llm.RoleUser, Content: "Keep the earlier requirements and unfinished work."}}
			got, err := SummarizeForCompaction(context.Background(), p, messages)
			if tc.tooLong {
				if got != "" || !errors.Is(err, errCompactionSummaryTooLong) {
					t.Fatalf("oversized completed summary accepted: bytes=%d error=%v", len(got), err)
				}
			} else if err != nil || got != tc.want || !utf8.ValidString(got) {
				t.Fatalf("accepted summary bytes changed: got=%d want=%d error=%v", len(got), len(tc.want), err)
			}
			if strings.Contains(got, "[summary truncated]") {
				t.Fatal("new summary was silently truncated")
			}
			if atomic.LoadInt32(&p.calls) != 1 || len(p.reqs) != 1 || len(p.reqs[0]) != 2 || p.toolReqs[0] != 0 || p.reqs[0][0].Content != compactionPrompt || p.reqs[0][1].Content != RenderCompactTranscript(messages) {
				t.Fatal("byte limit changed the one-call text-only summary request")
			}
		})
	}
}

func TestSummarizeForCompactionOverlongDrainsFinalUsage(t *testing.T) {
	var calls atomic.Int32
	body, err := json.Marshal(compactionByteLimitText(compactSummaryMaxChars + 1))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", body)
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		w.(http.Flusher).Flush()
		// Usage arrives after the completed content. Rejecting on size must
		// still consume it, rather than canceling the paid helper request.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":123,\"completion_tokens\":1200,\"total_tokens\":1323,\"prompt_tokens_details\":{\"cached_tokens\":23},\"completion_tokens_details\":{\"reasoning_tokens\":400}}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	raw, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "summary-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	stats := make(chan llm.CallStat, 1)
	p := llm.Metered(raw, "fixture", llm.PurposeMain, func(stat llm.CallStat) { stats <- stat })
	got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "Preserve the task requirements."}})
	if got != "" || !errors.Is(err, errCompactionSummaryTooLong) || calls.Load() != 1 {
		t.Fatalf("overlong summary acceptance/retry: bytes=%d error=%v requests=%d", len(got), err, calls.Load())
	}
	select {
	case stat := <-stats:
		if stat.TokensIn != 123 || stat.TokensOut != 1200 || stat.TokensCached != 23 || stat.TokensReasoning != 400 || stat.Canceled || stat.Failed || stat.Purpose != llm.PurposeCompact {
			t.Fatalf("overlong helper lost final usage or was canceled: %+v", stat)
		}
	case <-time.After(time.Second):
		t.Fatal("meter did not record the completed overlong helper")
	}
}

func TestAutoSummarizerOverlongSideFallback(t *testing.T) {
	valid := "Goal: continue work\nDone: source verified\nState: Preserve earlier restrictions.\nPending: Continue unresolved work."
	transport := errors.New("synthetic fallback transport failure")
	cases := []struct {
		name   string
		script []llm.Delta
		want   error
	}{
		{"valid-main", []llm.Delta{{Content: valid, FinishReason: "stop"}}, nil},
		{"overlong-main", []llm.Delta{{Content: compactionByteLimitText(compactSummaryMaxChars + 1), FinishReason: "stop"}}, errCompactionSummaryTooLong},
		{"failed-main", []llm.Delta{{Err: transport}}, transport},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			side := &stubProvider{name: "side", scripts: [][]llm.Delta{{{Content: compactionByteLimitText(compactSummaryMaxChars + 1), FinishReason: "stop"}}}}
			main := &stubProvider{name: "main", scripts: [][]llm.Delta{tc.script}}
			messages := []llm.Message{{Role: llm.RoleUser, Content: "Preserve all earlier restrictions and finish the work."}}
			got, err := NewAutoSummarizerWithProvider(side, nil)(context.Background(), main, messages)
			if tc.want == nil {
				if err != nil || got != WrapCompactSummary(valid) {
					t.Fatalf("valid active fallback changed: error=%v bytes=%d", err, len(got))
				}
			} else if got != "" || !errors.Is(err, tc.want) || !errors.Is(err, errCompactionSummaryTooLong) {
				t.Fatalf("failed fallback accepted partial text or lost error identity: bytes=%d error=%v", len(got), err)
			}
			if atomic.LoadInt32(&side.calls) != 1 || atomic.LoadInt32(&main.calls) != 1 || !reflect.DeepEqual(side.reqs, main.reqs) || len(side.toolReqs) != 1 || side.toolReqs[0] != 0 || main.toolReqs[0] != 0 {
				t.Fatal("overlong side model changed request bytes or retried beyond the existing one active fallback")
			}
		})
	}
}

func TestOverlongCompactionPreservesRunAndManualProjection(t *testing.T) {
	for _, mode := range []string{"automatic", "overflow", "manual"} {
		for _, useSide := range []bool{false, true} {
			name := mode + map[bool]string{false: "/main", true: "/side-and-main"}[useSide]
			t.Run(name, func(t *testing.T) {
				overlong := compactionByteLimitText(compactSummaryMaxChars + 1)
				scripts := [][]llm.Delta{
					{{Content: overlong, FinishReason: "stop"}},
					{{Content: "must not continue after rejecting task memory", FinishReason: "stop"}},
				}
				window, wantMainCalls := 100, int32(1)
				if mode == "overflow" {
					window, wantMainCalls = 100_000, 2
					scripts = append([][]llm.Delta{{{Err: errors.New("context length is 4096 tokens")}}}, scripts...)
				}
				main := &stubProvider{name: "fixture", scripts: scripts}
				var side *stubProvider
				summarize := NewAutoSummarizer(nil)
				if useSide {
					side = &stubProvider{name: "side", scripts: [][]llm.Delta{{{Content: overlong, FinishReason: "stop"}}}}
					summarize = NewAutoSummarizerWithProvider(side, nil)
				}
				writer := &flakyWriter{}
				l := newCompactionFailureLoop(t, main, summarize, window, writer)
				expected := compactionFailureJSON(t, l.Messages)
				if mode == "manual" {
					event, err := l.CompactNow(context.Background())
					if !errors.Is(err, errCompactionSummaryTooLong) || event.Removed != 0 {
						t.Fatalf("manual overlong summary changed projection: error=%v removed=%d", err, event.Removed)
					}
				} else {
					expected = compactionFailureJSON(t, append(append([]llm.Message(nil), l.Messages...), llm.Message{Role: llm.RoleUser, Content: "current task"}))
					assertCompactionFailureEvents(t, compactionFailureEvents(t, l, context.Background()), errCompactionSummaryTooLong, "")
				}
				assertCompactionFailureHistory(t, l, expected, writer)
				if atomic.LoadInt32(&main.calls) != wantMainCalls || len(main.reqs) != int(wantMainCalls) || (side != nil && atomic.LoadInt32(&side.calls) != 1) {
					t.Fatalf("rejected summary caused extra inference: main=%d side=%v", main.calls, useSide)
				}
				if main.toolReqs[wantMainCalls-1] != 0 || len(main.reqs[wantMainCalls-1]) != 2 || main.reqs[wantMainCalls-1][0].Content != compactionPrompt {
					t.Fatal("rejected summary changed its text-only request contract")
				}
				if side != nil && !reflect.DeepEqual(side.reqs[0], main.reqs[wantMainCalls-1]) {
					t.Fatal("side fallback lost history or changed helper request bytes")
				}
			})
		}
	}
}
