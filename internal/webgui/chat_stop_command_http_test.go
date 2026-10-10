package webgui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"supercli/internal/system/childproc"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
)

type stopHTTPCommandProvider struct {
	command    []string
	calls      atomic.Int32
	nextPrompt string
}

func (p *stopHTTPCommandProvider) Name() string { return "stop-http-command" }
func (p *stopHTTPCommandProvider) Complete(ctx context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	n := p.calls.Add(1)
	out := make(chan llm.Delta, 2)
	if n == 1 {
		raw, err := json.Marshal(map[string]any{"command": p.command, "timeout_ms": 30000, "max_stdout_kb": 8})
		if err != nil {
			return nil, err
		}
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "blocked-curl", Name: "ctx_execute", Arguments: string(raw)}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == llm.RoleUser {
				p.nextPrompt = messages[i].Content
				break
			}
		}
		out <- llm.Delta{Content: "after stop response", Usage: &llm.Usage{Input: 7, Output: 3}}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

// Exercise actual TCP disconnect and the actual ctx_execute child process,
// rather than canceling an injected handler context or manually releasing a
// fake completion receipt. No internet, sleeps or progress polling are used.
func TestChatHTTPStopKillsCommandAndNextPromptReachesProvider(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("native curl is unavailable")
	}
	srv := newTestServer(t, false)
	// Real projects are separate from the portable application data directory.
	srv.eng.setHome(t.TempDir())
	if _, err := srv.eng.sessionStore(); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.eng.newLoop(); err != nil {
		t.Fatal(err)
	}
	commandEntered := make(chan struct{})
	commandCanceled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce, enterOnce, cancelOnce sync.Once
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enterOnce.Do(func() { close(commandEntered) })
		select {
		case <-r.Context().Done():
			cancelOnce.Do(func() { close(commandCanceled) })
		case <-release:
		}
	}))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); target.Close() })
	provider := &stopHTTPCommandProvider{command: []string{curl, "--disable", "--noproxy", "*", "-s", target.URL}}
	srv.eng.mu.Lock()
	srv.eng.prov = provider
	srv.eng.mu.Unlock()
	api := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { api.CloseClientConnections(); api.Close() })
	client := api.Client()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	raw, _ := json.Marshal(chatRequest{Prompt: "Fetch the local fixture using ctx_execute.", TurnID: "http-stop-command"})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, api.URL+"/api/chat", strings.NewReader(string(raw)))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("chat status=%d", response.StatusCode)
	}
	// Consume SSE exactly as a client does before stopping an active command.
	startedFrame := make(chan struct{})
	readerDone := make(chan struct{})
	var captured strings.Builder
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			captured.WriteString(line + "\n")
			if strings.HasPrefix(line, "data:") {
				var event wireEvent
				if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) == nil && event.Type == "tool_call" && event.ID == "blocked-curl" {
					select {
					case <-startedFrame:
					default:
						close(startedFrame)
					}
				}
			}
		}
	}()
	waitCompletionTest(t, startedFrame, "actual command tool event")
	select {
	case <-commandEntered:
	case <-readerDone:
		t.Fatalf("command did not reach local fixture: %s", captured.String())
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for curl TCP request")
	}
	stopAt := time.Now()
	cancel()
	response.Body.Close()
	waitCompletionTest(t, readerDone, "client SSE abort")
	waitCompletionTest(t, commandCanceled, "actual curl socket closed after Stop")
	receiptCtx, stopReceipt := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopReceipt()
	receiptRequest, _ := http.NewRequestWithContext(receiptCtx, http.MethodGet, api.URL+"/api/chat/completion?id=http-stop-command", nil)
	completed, err := client.Do(receiptRequest)
	if err != nil {
		t.Fatalf("durable Stop receipt: %v", err)
	}
	var receipt struct {
		SessionID string
		Accepted  bool
	}
	// Decode with the wire keys without adding test-specific tags.
	var wire map[string]any
	decodeErr := json.NewDecoder(completed.Body).Decode(&wire)
	completed.Body.Close()
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	receipt.SessionID, _ = wire["session_id"].(string)
	receipt.Accepted, _ = wire["accepted"].(bool)
	if completed.StatusCode != http.StatusOK || !receipt.Accepted || receipt.SessionID == "" {
		t.Fatalf("receipt status=%d body=%+v", completed.StatusCode, wire)
	}
	elapsed := time.Since(stopAt)
	raw, _ = json.Marshal(chatRequest{Prompt: "Reply after Stop without rerunning curl.", TurnID: "http-after-stop", SessionID: receipt.SessionID})
	nextRequest, _ := http.NewRequestWithContext(receiptCtx, http.MethodPost, api.URL+"/api/chat", strings.NewReader(string(raw)))
	next, err := client.Do(nextRequest)
	if err != nil {
		t.Fatalf("next prompt: %v", err)
	}
	body, err := io.ReadAll(next.Body)
	next.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if next.StatusCode != http.StatusOK || !strings.Contains(string(body), "after stop response") || !strings.Contains(string(body), "\"type\":\"done\"") {
		t.Fatalf("next prompt did not finish: status=%d body=%s", next.StatusCode, body)
	}
	if provider.calls.Load() != 2 || !strings.Contains(provider.nextPrompt, "Reply after Stop") {
		t.Fatalf("provider calls=%d next=%q", provider.calls.Load(), provider.nextPrompt)
	}
	store, _ := srv.eng.sessionStore()
	messages, err := store.ReadMessages(context.Background(), receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var users []string
	for _, m := range messages {
		if m.Role == "user" {
			users = append(users, m.Content)
		}
	}
	if len(users) != 2 || !strings.Contains(users[1], "Reply after Stop") {
		t.Fatalf("durable history lost or duplicated next prompt: %+v", users)
	}
	t.Logf("real HTTP Stop killed curl, published durable receipt in %s and the next prompt completed", elapsed)
}

func TestChatGUIStopCommandAndSendUsingProductionJS(t *testing.T) {
	for _, mode := range []string{"running-command", "checkpoint-store-held"} {
		t.Run(mode, func(t *testing.T) {
			node := os.Getenv("SUPERCLI_TEST_NODE_EXE")
			if node == "" {
				var err error
				node, err = exec.LookPath("node")
				if err != nil {
					t.Skip("Node.js unavailable for production GUI integration")
				}
			}
			curl, err := exec.LookPath("curl")
			if err != nil {
				t.Skip("native curl unavailable")
			}
			srv := newTestServer(t, false)
			srv.eng.setHome(t.TempDir())
			if _, err := srv.eng.sessionStore(); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.eng.newLoop(); err != nil {
				t.Fatal(err)
			}
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var enterOnce, cancelOnce, releaseOnce sync.Once
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				enterOnce.Do(func() { close(entered) })
				select {
				case <-r.Context().Done():
					cancelOnce.Do(func() { close(canceled) })
				case <-release:
				}
			}))
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); target.Close() })
			provider := &stopHTTPCommandProvider{command: []string{curl, "--disable", "--noproxy", "*", "-s", target.URL}}
			srv.eng.mu.Lock()
			srv.eng.prov = provider
			srv.eng.mu.Unlock()
			var heldGate *checkpoint.StoreIO
			if mode == "checkpoint-store-held" {
				if _, err := srv.eng.checkpointManager(srv.eng.Home()); err != nil {
					t.Fatal(err)
				}
				gate, err := checkpoint.NewStoreGate(srv.eng.DataDir())
				if err != nil {
					t.Fatal(err)
				}
				held, err := gate.TryAcquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				heldGate = held
				t.Cleanup(func() { held.Close() })
			}
			production := srv.Handler()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var ready <-chan struct{}
				switch r.URL.Path {
				case "/fixture/command-ready":
					ready = entered
				case "/fixture/command-canceled":
					ready = canceled
				}
				if ready != nil {
					select {
					case <-ready:
						w.WriteHeader(http.StatusNoContent)
					case <-r.Context().Done():
					}
					return
				}
				production.ServeHTTP(w, r)
			}))
			t.Cleanup(func() {
				if heldGate != nil {
					heldGate.Close()
				}
				api.CloseClientConnections()
				api.Close()
			})
			script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "test-chat-stop-http.cjs"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, script, api.URL, mode)
			childproc.HideWindow(cmd)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("production GUI Stop→send failed: %v\n%s", err, output)
			}
			if provider.calls.Load() != 2 || !strings.Contains(provider.nextPrompt, "Reply after Stop") {
				t.Fatalf("next provider call missing: calls=%d prompt=%q", provider.calls.Load(), provider.nextPrompt)
			}
			if mode == "checkpoint-store-held" {
				select {
				case <-entered:
					t.Fatal("curl ran despite held checkpoint store")
				default:
				}
			}
			t.Logf("GUI phase timings:\n%s", output)
		})
	}
}
