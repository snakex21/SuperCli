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

func TestSummarizeForCompactionRejectsIncompleteStream(t *testing.T) {
	cases := []struct {
		name string
		end  llm.Delta
		want string
	}{
		{"token-limit", llm.Delta{FinishReason: "length"}, "length"},
		{"filtered", llm.Delta{FinishReason: "content_filter"}, "content_filter"},
		{"tool-finish", llm.Delta{FinishReason: "tool_calls"}, "tool_calls"},
		{"paused", llm.Delta{FinishReason: "pause_turn"}, "pause_turn"},
		{"tool-fragment", llm.Delta{ToolCall: &llm.ToolCall{ID: "unexpected", Name: "read_lines", Arguments: "{}"}}, "tool call"},
		{"provider-error", llm.Delta{Err: errors.New("transport failed")}, "transport failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubProvider{name: "summary", scripts: [][]llm.Delta{{
				{Content: "Goal: build the requested feature.\nDone: changed one file.\nState: "},
				tc.end,
				{Usage: &llm.Usage{Input: 150, Output: 40}},
			}}}
			got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) || got != "" {
				t.Fatalf("incomplete result accepted: summary=%q error=%v, want %q", got, err, tc.want)
			}
			if atomic.LoadInt32(&p.calls) != 1 || len(p.toolReqs) != 1 || p.toolReqs[0] != 0 {
				t.Fatalf("summary should make one text-only call: calls=%d tools=%v", p.calls, p.toolReqs)
			}
		})
	}
}

func TestSummarizeForCompactionKeepsCompletedText(t *testing.T) {
	want := "Goal: feature\nDone: implementation\nState: verified\nPending: review"
	for _, mode := range []string{"normal", "same-delta", "legacy-eof"} {
		t.Run(mode, func(t *testing.T) {
			script := []llm.Delta{{Reasoning: "private analysis"}, {Content: want}, {FinishReason: "stop"}, {Usage: &llm.Usage{Input: 10, Output: 10}}}
			switch mode {
			case "same-delta":
				script = []llm.Delta{{Content: want, FinishReason: "stop"}}
			case "legacy-eof":
				script = []llm.Delta{{Content: want}}
			}
			p := &stubProvider{name: "summary", scripts: [][]llm.Delta{script}}
			got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
			if err != nil || got != want || atomic.LoadInt32(&p.calls) != 1 {
				t.Fatalf("completed text changed or retried: got=%q err=%v calls=%d", got, err, p.calls)
			}
		})
	}
}

type compactionCompletionProvider struct {
	run func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error)
}

func newCompactionCompletionProvider(run func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error)) *compactionCompletionProvider {
	return &compactionCompletionProvider{run: run}
}

func (p *compactionCompletionProvider) Name() string         { return "summary-fixture" }
func (p *compactionCompletionProvider) SupportsVision() bool { return false }
func (p *compactionCompletionProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	return p.run(ctx, msgs, defs)
}

func TestSummarizeForCompactionCancelsRejectedProvider(t *testing.T) {
	var streamCtx context.Context
	p := newCompactionCompletionProvider(func(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
		streamCtx = ctx
		ch := make(chan llm.Delta, 2)
		ch <- llm.Delta{Content: "Goal: partial"}
		ch <- llm.Delta{Err: errors.New("failed")}
		close(ch)
		return ch, nil
	})
	got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
	if got != "" || err == nil {
		t.Fatalf("unexpected result: %q %v", got, err)
	}
	if !errors.Is(streamCtx.Err(), context.Canceled) {
		t.Fatal("abandoned provider request remains live")
	}
}

func TestSummarizeForCompactionCancellationRejectsPartialText(t *testing.T) {
	for _, closeStream := range []bool{true, false} {
		t.Run(map[bool]string{true: "closed-stream", false: "open-stream"}[closeStream], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch := make(chan llm.Delta, 1)
			p := newCompactionCompletionProvider(func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
				ch <- llm.Delta{Content: "Goal: canceled partial", FinishReason: "stop"}
				cancel()
				if closeStream {
					close(ch)
				}
				return ch, nil
			})
			type result struct {
				text string
				err  error
			}
			done := make(chan result, 1)
			go func() {
				text, err := SummarizeForCompaction(ctx, p, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
				done <- result{text, err}
			}()
			select {
			case got := <-done:
				if !closeStream {
					close(ch)
				}
				if got.text != "" || !errors.Is(got.err, context.Canceled) {
					t.Fatalf("cancellation accepted partial text: %+v", got)
				}
			case <-time.After(time.Second):
				if !closeStream {
					close(ch)
				}
				<-done
				t.Fatal("cancellation waited for provider channel closure")
			}
		})
	}
}

func TestAutoSummarizerTruncatedSideModelFallsBackOnce(t *testing.T) {
	side := &stubProvider{name: "side", scripts: [][]llm.Delta{{{Content: "Goal: incomplete"}, {FinishReason: "length"}}}}
	main := &stubProvider{name: "main", scripts: [][]llm.Delta{{{Content: "Goal: feature\nDone: complete summary\nState: verified\nPending: review"}, {FinishReason: "stop"}}}}
	got, err := NewAutoSummarizerWithProvider(side, nil)(context.Background(), main, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
	if err != nil || !strings.Contains(got, "complete summary") || strings.Contains(got, "incomplete") || atomic.LoadInt32(&side.calls) != 1 || atomic.LoadInt32(&main.calls) != 1 {
		t.Fatalf("fallback=%q err=%v calls side=%d main=%d", got, err, side.calls, main.calls)
	}
}

func TestAutoSummarizerCancellationDoesNotCallFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	side := newCompactionCompletionProvider(func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
		cancel()
		return nil, context.Canceled
	})
	main := newCompactionCompletionProvider(func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
		calls++
		return nil, context.Canceled
	})
	got, err := NewAutoSummarizerWithProvider(side, nil)(ctx, main, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
	if got != "" || !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled summary retried: %q %v calls=%d", got, err, calls)
	}
}

func TestCompactNowTruncatedSummaryKeepsHistory(t *testing.T) {
	p := &stubProvider{name: "summary", scripts: [][]llm.Delta{{{Content: "Goal: misleading partial\nDone: incomplete"}, {FinishReason: "length"}}}}
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "standing policy"},
		{Role: llm.RoleUser, Content: "older work"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("verified evidence ", 2000)},
		{Role: llm.RoleUser, Content: "previous correction"},
		{Role: llm.RoleAssistant, Content: "acknowledged"},
		{Role: llm.RoleUser, Content: "current request"},
	}
	l := &Loop{provider: p, windowFor: func(string) int { return 50_000 }, summarizer: NewAutoSummarizer(nil), Messages: append([]llm.Message(nil), history...)}
	before := l.VisibleMessages()
	ev, err := l.CompactNow(context.Background())
	if err == nil || !strings.Contains(err.Error(), "length") || ev.Removed != 0 || !reflect.DeepEqual(l.Messages, history) || !reflect.DeepEqual(l.VisibleMessages(), before) {
		t.Fatalf("truncated summary replaced history: removed=%d error=%v messages=%d", ev.Removed, err, len(l.Messages))
	}
	if atomic.LoadInt32(&p.calls) != 1 {
		t.Fatalf("unexpected extra calls: %d", p.calls)
	}
}

func TestClampSummaryPreservesUTF8Boundary(t *testing.T) {
	for _, r := range []string{"ż", "界", "🙂"} {
		for inside := 1; inside < len(r); inside++ {
			prefix := strings.Repeat("x", compactSummaryMaxChars-inside)
			text := prefix + r + "tail"
			got := ClampSummary(text)
			want := prefix + "\n[summary truncated]"
			if got != want || !utf8.ValidString(got) {
				t.Fatalf("split %d-byte rune at %d: valid=%v bytes=%d", len(r), inside, utf8.ValidString(got), len(got))
			}
		}
	}
	text := strings.Repeat("ż", compactSummaryMaxChars/2) + "tail"
	if got := ClampSummary(text); got != text[:compactSummaryMaxChars]+"\n[summary truncated]" {
		t.Fatal("intact boundary changed")
	}
}

func TestSummarizeForCompactionOpenAICompletionContract(t *testing.T) {
	for _, finish := range []string{"length", "stop"} {
		t.Run(finish, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					Tools    []json.RawMessage
					Messages []json.RawMessage
				}
				if r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Tools) != 0 || len(request.Messages) != 2 {
					t.Error("unexpected summary request path, body, or tools")
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Goal: requested feature\"}}]}\n\n")
				fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()
			p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "summary-fixture"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
			if finish == "length" {
				if err == nil || !strings.Contains(err.Error(), "length") || got != "" {
					t.Fatalf("accepted truncated SSE: %q %v", got, err)
				}
			} else if err != nil || got != "Goal: requested feature" {
				t.Fatalf("completed SSE changed: %q %v", got, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("requests=%d, want one", calls.Load())
			}
		})
	}
}

func TestAutoSummarizerCompletedSideModelDoesNotCallFallback(t *testing.T) {
	side := &stubProvider{name: "side", scripts: [][]llm.Delta{{{Content: "Goal: feature\nDone: complete summary\nState: verified\nPending: review"}, {FinishReason: "stop"}}}}
	main := &stubProvider{name: "main", scripts: [][]llm.Delta{{{Content: "unexpected fallback"}, {FinishReason: "stop"}}}}
	got, err := NewAutoSummarizerWithProvider(side, nil)(context.Background(), main, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
	if err != nil || !strings.Contains(got, "complete summary") || atomic.LoadInt32(&side.calls) != 1 || atomic.LoadInt32(&main.calls) != 0 {
		t.Fatalf("healthy side model retried: %q %v side=%d main=%d", got, err, side.calls, main.calls)
	}
}

func BenchmarkSummarizeForCompactionCompleted(b *testing.B) {
	p := newCompactionCompletionProvider(func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
		ch := make(chan llm.Delta, 2)
		ch <- llm.Delta{Content: "Goal: feature\nDone: implementation\nState: verified\nPending: review"}
		ch <- llm.Delta{FinishReason: "stop"}
		close(ch)
		return ch, nil
	})
	msgs := []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("verified older work ", 100)}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := SummarizeForCompaction(context.Background(), p, msgs); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSummarizeForCompactionDrainsFinalUsage(t *testing.T) {
	for _, finish := range []string{"length", "stop"} {
		t.Run(finish, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Goal: feature\"}}]}\n\n")
				fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Millisecond):
				}
				fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":123,\"completion_tokens\":45,\"total_tokens\":168,\"prompt_tokens_details\":{\"cached_tokens\":23},\"completion_tokens_details\":{\"reasoning_tokens\":10}}}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()
			raw, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "summary-fixture"})
			if err != nil {
				t.Fatal(err)
			}
			stats := make(chan llm.CallStat, 1)
			p := llm.Metered(raw, "fixture", llm.PurposeMain, func(s llm.CallStat) { stats <- s })
			got, err := SummarizeForCompaction(context.Background(), p, []llm.Message{{Role: llm.RoleUser, Content: "older work"}})
			if finish == "length" {
				if got != "" || err == nil || !strings.Contains(err.Error(), "length") {
					t.Fatalf("accepted truncated summary: %q %v", got, err)
				}
			} else if err != nil || got != "Goal: feature" {
				t.Fatalf("completed summary changed: %q %v", got, err)
			}
			select {
			case stat := <-stats:
				if stat.TokensIn != 123 || stat.TokensOut != 45 || stat.TokensCached != 23 || stat.TokensReasoning != 10 || stat.Canceled || stat.Purpose != llm.PurposeCompact {
					t.Fatalf("final usage was dropped or canceled: %+v", stat)
				}
			case <-time.After(time.Second):
				t.Fatal("meter did not finish recording the summary request")
			}
		})
	}
}
