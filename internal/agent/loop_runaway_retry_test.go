package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func runawayLoop(t *testing.T, runaways int) (*Loop, *stubProvider) {
	t.Helper()
	var scripts [][]llm.Delta
	for i := 0; i < runaways; i++ {
		scripts = append(scripts, []llm.Delta{{Err: &llm.RunawayToolCallError{Tool: "patch_file", KB: 19}}})
	}
	scripts = append(scripts, []llm.Delta{{Content: "Done."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 2, Total: 12}}})
	main := &stubProvider{name: "main", scripts: scripts}
	l, err := NewLoop(LoopConfig{Provider: main, Registry: tools.NewRegistry(), BaseDir: t.TempDir(), MaxSteps: 10})
	if err != nil {
		t.Fatal(err)
	}
	return l, main
}

func drainRunaway(t *testing.T, l *Loop) (answer string, runErr error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := l.Run(ctx, "rename Foo to Bar")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		switch e := ev.(type) {
		case MessageEvent:
			answer += e.Text
		case ErrorEvent:
			runErr = e.Err
		}
	}
	return answer, runErr
}

func TestRunawayToolCallIsRequestedAgain(t *testing.T) {
	l, main := runawayLoop(t, 2)
	answer, err := drainRunaway(t, l)
	if err != nil || !strings.Contains(answer, "Done.") {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if got := atomic.LoadInt32(&main.calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
	notes := 0
	for _, m := range l.Messages {
		if strings.HasPrefix(m.Content, "[runaway tool call]") {
			notes++
		}
	}
	if notes != 2 {
		t.Fatalf("runaway notes = %d, want 2", notes)
	}
}

func TestRunawayToolCallRetriesAreBounded(t *testing.T) {
	l, main := runawayLoop(t, maxRunawayRetries+1)
	_, err := drainRunaway(t, l)
	var runaway *llm.RunawayToolCallError
	if !errors.As(err, &runaway) {
		t.Fatalf("want the runaway error after %d retries, got %v", maxRunawayRetries, err)
	}
	if got := atomic.LoadInt32(&main.calls); got != maxRunawayRetries+1 {
		t.Fatalf("calls = %d, want %d", got, maxRunawayRetries+1)
	}
}
