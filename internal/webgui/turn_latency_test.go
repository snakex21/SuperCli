package webgui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

// latencyProvider plays a scripted conversation: each Complete call sends one
// step's deltas and remembers when the last one was handed over.
type latencyProvider struct {
	mu        sync.Mutex
	steps     [][]llm.Delta
	call      int
	lastToken time.Time
}

func (p *latencyProvider) Name() string { return "latency-fixture" }
func (p *latencyProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	step := p.steps[min(p.call, len(p.steps)-1)]
	p.call++
	p.mu.Unlock()
	out := make(chan llm.Delta)
	go func() {
		defer close(out)
		for _, d := range step {
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
		p.mu.Lock()
		p.lastToken = time.Now()
		p.mu.Unlock()
	}()
	return out, nil
}

// stampWriter timestamps the first SSE event of each type as it is written.
type stampWriter struct {
	mu     sync.Mutex
	header http.Header
	buf    bytes.Buffer
	first  map[string]time.Time
}

func (w *stampWriter) Header() http.Header { return w.header }
func (w *stampWriter) WriteHeader(int)     {}
func (w *stampWriter) Flush()              {}
func (w *stampWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	w.buf.Write(b)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type != "" {
			if _, seen := w.first[ev.Type]; !seen {
				w.first[ev.Type] = now
			}
		}
	}
	return len(b), nil
}

// TestTurnEndLatency measures the time from the model's last token to the
// moment the GUI may take the next message (the stream closes: a message
// typed after "finishing" is sent only then), and to "finishing" / "done",
// for a plain answer, an answer after a read tool and answers after a file
// write in a small and a 3000-file project. Run with -v for the turn-end
// timing log of every step.
func TestTurnEndLatency(t *testing.T) {
	answer := []llm.Delta{{Content: "Gotowe: "}, {Content: "odpowiedź."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 100, Output: 5}}}
	writeStep := [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "w", Name: "write_file", Arguments: `{"path":"notes.txt","content":"hello\n"}`}}, {FinishReason: "tool_calls"}},
		answer,
	}
	cases := []struct {
		name  string
		files int // project size before the turn
		steps [][]llm.Delta
	}{
		{"plain answer", 0, [][]llm.Delta{answer}},
		{"after tools", 0, [][]llm.Delta{
			{{ToolCall: &llm.ToolCall{ID: "ls", Name: "list_dir", Arguments: `{"path":"."}`}}, {FinishReason: "tool_calls"}},
			answer,
		}},
		{"after a file write", 0, writeStep},
		{"after a file write, 3000-file project", 3000, writeStep},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < c.files; i++ {
				sub := filepath.Join(dir, "src", fmt.Sprintf("pkg%02d", i%40))
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%04d.go", i)), []byte(strings.Repeat("package x\n", 50)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			eng, err := NewEngine(echoConfig(), dir, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			srv := NewServer(eng, false)
			if _, err := srv.eng.sessionStore(); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.eng.newLoop(); err != nil {
				t.Fatal(err)
			}
			agent.SetTurnTiming(testing.Verbose())
			defer agent.SetTurnTiming(false)
			provider := &latencyProvider{steps: c.steps}
			srv.eng.mu.Lock()
			srv.eng.prov = provider
			srv.eng.mu.Unlock()
			w := &stampWriter{header: http.Header{}, first: map[string]time.Time{}}
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"zrób to","turn_id":"latency-`+fmt.Sprint(c.files)+`-`+fmt.Sprint(len(c.steps))+`"}`))
			srv.handleChat(w, req)
			closed := time.Now()
			provider.mu.Lock()
			last := provider.lastToken
			provider.mu.Unlock()
			w.mu.Lock()
			finishing, done := w.first["finishing"], w.first["done"]
			w.mu.Unlock()
			if last.IsZero() || done.IsZero() || finishing.IsZero() {
				t.Fatalf("no last token, finishing or done event:\n%s", w.buf.String())
			}
			t.Logf("%s: last token -> finishing %s, -> done %s, -> next message can be sent %s",
				c.name, finishing.Sub(last).Round(time.Millisecond), done.Sub(last).Round(time.Millisecond), closed.Sub(last).Round(time.Millisecond))
			if d := finishing.Sub(last); d > 300*time.Millisecond {
				t.Errorf("%s: the composer waited %s after the last token", c.name, d)
			}
			if d := closed.Sub(last); d > 1500*time.Millisecond {
				t.Errorf("%s: the next message waited %s after the last token", c.name, d)
			}
		})
	}
}
