package webgui

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/childproc"
)

// Real native child, actual HTTP Handler and production readSSE/chat/transcript
// JS; only the model is a deterministic tool-call stub.
type commandEndHTTPProvider struct {
	command     []string
	callID      string
	calls       atomic.Int32
	mu          sync.Mutex
	toolInputs  []string
	nextPrompts []string
}

func (p *commandEndHTTPProvider) Name() string { return "command-end-http" }
func (p *commandEndHTTPProvider) Complete(ctx context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := p.calls.Add(1)
	out := make(chan llm.Delta, 2)
	if n == 1 {
		raw, err := json.Marshal(map[string]any{"command": p.command, "timeout_ms": 10000, "max_stdout_kb": 8})
		if err != nil {
			return nil, err
		}
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: p.callID, Name: "ctx_execute", Arguments: string(raw)}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		p.mu.Lock()
		if n == 2 {
			for _, message := range messages {
				if message.Role == llm.RoleTool && message.ToolCallID == p.callID {
					p.toolInputs = append(p.toolInputs, message.TextOnly().Content)
				}
			}
		} else {
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == llm.RoleUser {
					p.nextPrompts = append(p.nextPrompts, messages[i].TextOnly().Content)
					break
				}
			}
		}
		p.mu.Unlock()
		text := "command-end response"
		if n >= 3 {
			text = "next-command-end response"
		}
		out <- llm.Delta{Content: text, Usage: &llm.Usage{Input: 7, Output: 3}}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func commandEndHTTPRoot(t *testing.T) (string, string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, ".tmp", "optimization-oct8-2026", "closure-audit", "command-end-data")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	return root, dir
}

func TestChatCommandEndHTTPProductionGUI(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native cmd regression requires Windows")
	}
	node := os.Getenv("SUPERCLI_TEST_NODE_EXE")
	if node == "" {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			t.Skip("Node.js unavailable for production GUI integration")
		}
	}
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Fatal("native cmd unavailable: ", err)
	}
	for _, mode := range []string{"cmd-if-exist", "curl-gif", "cmd-error", "checkpoint-rejected"} {
		t.Run(mode, func(t *testing.T) {
			root, dir := commandEndHTTPRoot(t)
			home, data := filepath.Join(dir, "workspace"), filepath.Join(dir, "data")
			for _, path := range []string{home, data, filepath.Join(home, "Downloads")} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			// App DB/checkpoint data are a sibling of the project, never captured
			// as project artifacts. Preserve the real checkpoint/drain lifecycle.
			if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("navigator = \"off\"\nmax_steps = 4\npreflight_repo = false\n"), 0600); err != nil {
				t.Fatal(err)
			}
			eng, err := NewEngine(echoConfig(), home, data)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			provider := &commandEndHTTPProvider{callID: "command-end-" + mode}
			var gifBytes bytes.Buffer
			if err := gif.Encode(&gifBytes, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}), nil); err != nil {
				t.Fatal(err)
			}
			var downloads atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/fixture.gif" {
					http.NotFound(w, r)
					return
				}
				downloads.Add(1)
				w.Header().Set("Content-Type", "image/gif")
				_, _ = w.Write(gifBytes.Bytes())
			}))
			t.Cleanup(target.Close)
			downloadPath := filepath.Join(home, "Downloads", "downloaded.gif")
			switch mode {
			case "cmd-if-exist":
				fixturePath := filepath.Join(home, "Downloads")
				// Preserve reported argv; production configureCommandLine adds /d.
				provider.command = []string{"cmd", "/c", "if exist " + fixturePath + " (echo Downloads) else (echo brak)"}
			case "curl-gif":
				curl, err := exec.LookPath("curl")
				if err != nil {
					t.Skip("native curl unavailable")
				}
				provider.command = []string{curl, "--disable", "--noproxy", "*", "--fail", "--silent", "--show-error", "--output", downloadPath, target.URL + "/fixture.gif"}
			case "cmd-error":
				provider.command = []string{"cmd", "/c", "exit /b 7"}
			case "checkpoint-rejected":
				// Metadata-only over-limit fixture; checkpoint must reject before
				// running the command, then settle its GUI row and active owner.
				large, err := os.Create(filepath.Join(home, "large-archive.7z"))
				if err != nil {
					t.Fatal(err)
				}
				truncateErr := large.Truncate((256 << 20) + 1)
				closeErr := large.Close()
				if truncateErr != nil {
					t.Fatal(truncateErr)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				provider.command = []string{"cmd", "/c", "echo unexpected > unexpected.txt"}
			}
			eng.mu.Lock()
			eng.prov = provider
			eng.mu.Unlock()
			srv := NewServer(eng, false)
			production := srv.Handler()
			runCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/fixture/active-work" {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"activeWork": eng.HasActiveWork()})
					return
				}
				production.ServeHTTP(w, r)
			}))
			api.Config.BaseContext = func(net.Listener) context.Context { return runCtx }
			api.Start()
			t.Cleanup(func() { api.CloseClientConnections(); api.Close() })
			script := filepath.Join(root, "scripts", "test-chat-command-end-http.cjs")
			cmd := exec.CommandContext(runCtx, node, script, api.URL, mode, provider.callID)
			childproc.HideWindow(cmd)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("normal native command→GUI completion failed: %v\n%s", err, output)
			}
			var report map[string]any
			const marker = "command-end-result="
			var reportJSON string
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(line, marker) {
					reportJSON = strings.TrimPrefix(line, marker)
				}
			}
			if reportJSON == "" || json.Unmarshal([]byte(reportJSON), &report) != nil {
				t.Fatalf("missing GUI result: %s", output)
			}
			sessionID, _ := report["session_id"].(string)
			if sessionID == "" {
				t.Fatalf("missing durable session: %s", output)
			}
			if eng.HasActiveWork() {
				t.Fatal("finished native command remains active; close would request confirmation")
			}
			provider.mu.Lock()
			toolInputs := append([]string(nil), provider.toolInputs...)
			nextPrompts := append([]string(nil), provider.nextPrompts...)
			provider.mu.Unlock()
			if provider.calls.Load() != 3 || len(toolInputs) != 1 || len(nextPrompts) != 1 || nextPrompts[0] != "Reply after completed command without rerunning it." {
				t.Fatalf("unexpected foreground lifecycle: calls=%d tools=%v next=%v", provider.calls.Load(), toolInputs, nextPrompts)
			}
			if mode == "cmd-if-exist" && !strings.Contains(toolInputs[0], "Downloads") {
				t.Fatalf("native conditional lost stdout: %s", toolInputs[0])
			}
			if mode == "cmd-error" && !strings.Contains(toolInputs[0], "7") {
				t.Fatalf("native exit code lost: %s", toolInputs[0])
			}
			if mode == "checkpoint-rejected" {
				if !strings.Contains(toolInputs[0], "checkpoint snapshot limit exceeded") || !strings.Contains(toolInputs[0], "tool did not run") {
					t.Fatalf("checkpoint cause lost: %s", toolInputs[0])
				}
				if _, err := os.Stat(filepath.Join(home, "unexpected.txt")); !os.IsNotExist(err) {
					t.Fatal("rejected command ran", err)
				}
			}
			if mode == "curl-gif" {
				got, err := os.ReadFile(downloadPath)
				if err != nil {
					t.Fatal(err)
				}
				if downloads.Load() != 1 || !bytes.Equal(got, gifBytes.Bytes()) {
					t.Fatalf("curl did not preserve exact GIF bytes: requests=%d bytes=%d expected=%d", downloads.Load(), len(got), gifBytes.Len())
				}
				if _, err := gif.Decode(bytes.NewReader(got)); err != nil {
					t.Fatalf("downloaded GIF invalid: %v", err)
				}
			}
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			messages, err := store.ReadMessages(runCtx, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			var users, tools int
			for _, message := range messages {
				if message.Role == "user" {
					users++
				}
				if message.Role == "tool" && message.ToolCallID == provider.callID {
					tools++
				}
			}
			if users != 2 || tools != 1 {
				t.Fatalf("durable transcript duplicated or lost turn/tool result: users=%d tools=%d", users, tools)
			}
			t.Logf("normal native completion: %s", output)
		})
	}
}
