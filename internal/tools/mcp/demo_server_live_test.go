package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

// TestDemoServerLive builds examples/mcp/demo and drives it through the real
// portable workspace, Manager and mcp_bridge.
func TestDemoServerLive(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	dataDir := t.TempDir()
	pkgDir := filepath.Join(dataDir, PortableDirName, "demo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(pkgDir, "demo-mcp")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	build := exec.Command(goBin, "build", "-o", exe, "./examples/mcp/demo")
	build.Dir = filepath.Join("..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build demo: %v\n%s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "mcp", "demo", ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, ManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	configs, packages, err := LoadWorkspace(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := configs["demo"]; !ok {
		t.Fatalf("demo package not available: %+v", packages)
	}
	manager := NewManager(map[string]ServerConfig{"demo": configs["demo"]})
	t.Cleanup(manager.StopAll)
	bridge := NewBridge(manager).Spec()
	ctx, cancelAll := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelAll()
	call := func(ctx context.Context, args map[string]any) core.Result {
		raw, _ := json.Marshal(args)
		res, err := bridge.Fn(ctx, raw)
		if err != nil {
			t.Fatalf("bridge %v: %v", args, err)
		}
		return res
	}

	// Search uses declared tools without starting the process.
	search := call(ctx, map[string]any{"action": "search", "server": "demo"})
	var payload struct {
		Tools []struct {
			Name     string `json:"name"`
			ReadOnly bool   `json:"read_only"`
			Declared bool   `json:"declared"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(search.Text), &payload); err != nil || len(payload.Tools) != 5 {
		t.Fatalf("search = %s (%v)", search.Text, err)
	}
	for _, tool := range payload.Tools {
		if !tool.Declared || tool.ReadOnly != (tool.Name != "demo_write") {
			t.Fatalf("declared tool %+v", tool)
		}
	}
	if s, _ := manager.Get("demo"); s.Status().Running {
		t.Fatal("search started the declared server")
	}

	// demo_image returns a PNG.
	img := call(ctx, map[string]any{"action": "call", "server": "demo", "tool": "demo_image"})
	all := append(append(append([]*core.ImageContent{}, img.Images...), img.PreviewImages...), img.Files...)
	if img.Err != nil || len(all) != 1 || all[0].MediaType != "image/png" || len(all[0].Data) == 0 {
		t.Fatalf("demo_image = %+v", img)
	}

	// demo_slow emits progress.
	var mu sync.Mutex
	var progress []core.ToolProgress
	pctx := core.WithToolProgress(ctx, func(p core.ToolProgress) { mu.Lock(); progress = append(progress, p); mu.Unlock() })
	slow := call(pctx, map[string]any{"action": "call", "server": "demo", "tool": "demo_slow", "arguments": map[string]any{"seconds": 2}})
	mu.Lock()
	if slow.Err != nil || !strings.Contains(slow.Text, "done") || len(progress) != 2 || progress[1].Message != "krok 2/2" || progress[1].Total != 2 {
		t.Fatalf("demo_slow = %+v progress=%+v", slow, progress)
	}
	mu.Unlock()

	// Cancellation stops waiting quickly and the server keeps working.
	cctx, cancel := context.WithCancel(ctx)
	time.AfterFunc(1200*time.Millisecond, cancel)
	start := time.Now()
	cancelled := call(cctx, map[string]any{"action": "call", "server": "demo", "tool": "demo_slow", "arguments": map[string]any{"seconds": 30}})
	if cancelled.Err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled call = %+v after %v", cancelled, time.Since(start))
	}

	// demo_session receives the conversation id and counts per session.
	sctx := llm.WithOpenCodeSession(ctx, "ses_demo_1")
	for i, want := range []string{"calls=1", "calls=2"} {
		res := call(sctx, map[string]any{"action": "call", "server": "demo", "tool": "demo_session"})
		if res.Err != nil || !strings.Contains(res.Text, "session_id=ses_demo_1") || !strings.Contains(res.Text, want) {
			t.Fatalf("demo_session #%d = %+v", i, res)
		}
	}

	// demo_big is retained behind a preview.
	big := call(ctx, map[string]any{"action": "call", "server": "demo", "tool": "demo_big"})
	if big.Err != nil || len(big.RetainedText) < 200*1024 || len(big.Text) >= len(big.RetainedText) {
		t.Fatalf("demo_big text=%d retained=%d err=%v", len(big.Text), len(big.RetainedText), big.Err)
	}
}
