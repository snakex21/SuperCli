package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A stream that breaks after output but before the usage frame reports an
// Estimated usage (input = local request estimate, output = non-whitespace
// streamed bytes / 3) before the error, so cost accounting counts it. A stream
// that did send usage reports the provider's numbers, not an estimate.
func TestAbortedStreamReportsEstimatedUsage(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130TextStream(wire, w, "hello ", "world")
				time.Sleep(20 * time.Millisecond)
				a130Abort()
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			ch, err := p.Complete(context.Background(), []Message{{Role: RoleSystem, Content: "be brief"}, {Role: RoleUser, Content: strings.Repeat("question ", 40)}}, a130Tools)
			if err != nil {
				t.Fatal(err)
			}
			var usage *Usage
			var streamErr error
			for d := range ch {
				if d.Usage != nil {
					if streamErr != nil {
						t.Fatal("usage after the error")
					}
					usage = d.Usage
				}
				if d.Err != nil && streamErr == nil {
					streamErr = d.Err
				}
			}
			if streamErr == nil {
				t.Fatal("aborted stream reported success")
			}
			if usage == nil || !usage.Estimated || usage.Output != 4 || usage.Input <= 40 || usage.Total != usage.Input+usage.Output {
				t.Fatalf("estimated usage = %+v (err=%v)", usage, streamErr)
			}
		})
	}
}

func TestRunawayStreamReportsEstimatedUsage(t *testing.T) {
	for _, wire := range a130Wires {
		t.Run(string(wire), func(t *testing.T) {
			m := newA130Mock(t, func(w http.ResponseWriter, r *http.Request, n int) {
				a130ToolStart(wire, w, `{"path":"a","content":"`)
				for i := 0; i < 400 && r.Context().Err() == nil; i++ {
					if !a130ToolArgs(wire, w, strings.Repeat("loop-", 20)) {
						return
					}
				}
				a130Finish(wire, w)
			})
			p, _ := a130Provider(t, wire, m.URL(), 5*time.Second)
			res := a130Run(t, context.Background(), p, 10*time.Second)
			var runaway *RunawayToolCallError
			if !errors.As(res.Err, &runaway) {
				t.Fatalf("err=%v", res.Err)
			}
			// a130Run does not collect usage; re-run collecting it.
			ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, a130Tools)
			if err != nil {
				t.Fatal(err)
			}
			var usage *Usage
			for d := range ch {
				if d.Usage != nil {
					usage = d.Usage
				}
			}
			if usage == nil || !usage.Estimated || usage.Output < 5000 {
				t.Fatalf("runaway usage = %+v", usage)
			}
		})
	}
}
