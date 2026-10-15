package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

type silentCommandProgressProvider struct {
	command    []string
	timeoutMS  int
	stall      bool
	calls      atomic.Int32
	toolResult string
	resumed    chan struct{}
}

func (p *silentCommandProgressProvider) Name() string { return "silent-command-progress" }

func (p *silentCommandProgressProvider) Complete(ctx context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := p.calls.Add(1)
	if n > 2 {
		return nil, errors.New("fixture command was unexpectedly retried")
	}
	out := make(chan llm.Delta, 2)
	if n == 1 {
		raw, err := json.Marshal(map[string]any{"command": p.command, "timeout_ms": p.timeoutMS})
		if err != nil {
			return nil, err
		}
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "silent-command", Name: "ctx_execute", Arguments: string(raw)}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		for _, message := range messages {
			if message.Role == llm.RoleTool && message.ToolCallID == "silent-command" {
				p.toolResult = message.TextOnly().Content
			}
		}
		close(p.resumed)
		if p.stall {
			return stalledWebProvider{}.Complete(ctx, messages, nil)
		}
		out <- llm.Delta{Content: "silent command finished", Usage: &llm.Usage{Input: 7, Output: 3}}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

// The native command stays silent on a local HTTP socket beyond the model's
// idle limit. Exercise the actual GUI handler, command runner, checkpoint and
// durable completion path, with no model service or network polling.
func TestChatSilentCommandDoesNotConsumeModelProgressTimeout(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("native curl is unavailable")
	}
	for _, mode := range []string{"complete", "stalled-model-after-command", "stop", "explicit-command-deadline"} {
		t.Run(mode, func(t *testing.T) {
			srv := newTestServer(t, false)
			srv.eng.setHome(t.TempDir())
			if _, err := srv.eng.sessionStore(); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.eng.newLoop(); err != nil {
				t.Fatal(err)
			}
			const modelIdle = 200 * time.Millisecond
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var enterOnce, cancelOnce, releaseOnce sync.Once
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				enterOnce.Do(func() { close(entered) })
				select {
				case <-r.Context().Done():
					cancelOnce.Do(func() { close(canceled) })
				case <-release:
					_, _ = w.Write([]byte("command-proof"))
				}
			}))
			t.Cleanup(target.Close)
			provider := &silentCommandProgressProvider{
				command: []string{curl, "--disable", "--noproxy", "*", "--silent", "--show-error", target.URL},
				stall:   mode == "stalled-model-after-command", resumed: make(chan struct{}),
			}
			if mode == "explicit-command-deadline" {
				provider.timeoutMS = int((4 * modelIdle).Milliseconds())
			}
			srv.eng.mu.Lock()
			srv.eng.prov, srv.eng.cfg.Timeout = provider, modelIdle
			srv.eng.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			finished := make(chan struct{})
			recorder := httptest.NewRecorder()
			t.Cleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
				waitCompletionTest(t, finished, "silent command cleanup")
			})
			request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"Execute the fixture command once.","turn_id":"silent-command-progress"}`)).WithContext(ctx)
			go func() {
				defer close(finished)
				srv.handleChat(recorder, request)
			}()
			if mode == "explicit-command-deadline" {
				// Startup is part of the command deadline too. Under load curl
				// may legitimately time out before reaching the HTTP fixture;
				// require retained timeout evidence, not a pre-timeout connection.
				waitCompletionTest(t, finished, "explicit command deadline")
				if provider.calls.Load() != 2 || !strings.Contains(provider.toolResult, "deadline exceeded") {
					t.Fatalf("explicit deadline was not honored: calls=%d result=%q body=%s", provider.calls.Load(), provider.toolResult, recorder.Body.String())
				}
			} else {
				waitCompletionTest(t, entered, "silent native command")
				// Await a single timer, not repeated progress checks. Before the fix
				// the GUI ends the request at modelIdle while curl is still alive.
				select {
				case <-finished:
					t.Fatalf("GUI interrupted a running command: %s", recorder.Body.String())
				case <-time.After(4 * modelIdle):
				}
				if mode == "stop" {
					cancel()
					waitCompletionTest(t, finished, "Stop after silent command")
					waitCompletionTest(t, canceled, "native command socket closed")
					if provider.calls.Load() != 1 {
						t.Fatal("Stop resumed the model")
					}
				} else {
					releaseOnce.Do(func() { close(release) })
					waitCompletionTest(t, provider.resumed, "model resumed with retained tool result")
					waitCompletionTest(t, finished, "command and model completion")
					if provider.calls.Load() != 2 || !strings.Contains(provider.toolResult, "command-proof") {
						t.Fatalf("command result lost or repeated: calls=%d result=%q", provider.calls.Load(), provider.toolResult)
					}
				}
			}
			body := recorder.Body.String()
			if mode == "stalled-model-after-command" {
				if !strings.Contains(body, "provider produced no model progress") {
					t.Fatalf("model watchdog did not resume after command: %s", body)
				}
			} else if strings.Contains(body, "provider produced no model progress") {
				t.Fatalf("tool execution was treated as provider inactivity: %s", body)
			}
			if mode == "complete" || mode == "explicit-command-deadline" {
				if !strings.Contains(body, `"type":"done"`) || !strings.Contains(body, "silent command finished") {
					t.Fatalf("missing normal terminal response: %s", body)
				}
			}
			receipt := httptest.NewRecorder()
			srv.handleChatCompletion(receipt, httptest.NewRequest(http.MethodGet, "/api/chat/completion?id=silent-command-progress", nil))
			var completion struct {
				SessionID string `json:"session_id"`
				Accepted  bool   `json:"accepted"`
			}
			if receipt.Code != http.StatusOK || json.Unmarshal(receipt.Body.Bytes(), &completion) != nil || !completion.Accepted || completion.SessionID == "" {
				t.Fatalf("conversation completion lost: status=%d body=%s", receipt.Code, receipt.Body.String())
			}
			if srv.eng.HasActiveWork() {
				t.Fatal("finished command left active work behind")
			}
		})
	}
}
