package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// part2Server is a scripted MCP server: it interleaves progress
// notifications and a server->client ping before answering tools/call,
// records tools/call params and notifications, and can block until cancelled.
type part2Server struct {
	mu        sync.Mutex
	calls     []json.RawMessage
	cancelled []json.RawMessage
	pong      bool
	block     chan struct{}
}

func (p *part2Server) serve(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	enc := json.NewEncoder(out)
	var encMu sync.Mutex
	send := func(v any) { encMu.Lock(); _ = enc.Encode(v); encMu.Unlock() }
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		if req.Method == "" { // response to our ping
			p.mu.Lock()
			p.pong = string(req.ID) == `"srv-1"` && req.Result != nil
			p.mu.Unlock()
			continue
		}
		if len(req.ID) == 0 {
			if req.Method == "notifications/cancelled" {
				p.mu.Lock()
				p.cancelled = append(p.cancelled, append(json.RawMessage(nil), req.Params...))
				p.mu.Unlock()
			}
			continue
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			resp["result"] = map[string]any{"protocolVersion": ProtocolVersion}
		case "tools/list":
			resp["result"] = map[string]any{"tools": []map[string]any{
				{"name": "look", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}},
				{"name": "wipe", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": true}},
				{"name": "plain", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			p.mu.Lock()
			p.calls = append(p.calls, append(json.RawMessage(nil), req.Params...))
			p.mu.Unlock()
			var params struct {
				Name string `json:"name"`
				Meta struct {
					Token string `json:"progressToken"`
				} `json:"_meta"`
			}
			_ = json.Unmarshal(req.Params, &params)
			send(map[string]any{"jsonrpc": "2.0", "id": "srv-1", "method": "ping"})
			for i := 1; i <= 2; i++ {
				send(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{
					"progressToken": params.Meta.Token, "progress": i, "total": 4, "message": "step"}})
			}
			if params.Name == "slow" && p.block != nil {
				go func(id json.RawMessage) { <-p.block }(req.ID)
				continue // never answers
			}
			resp["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok:" + params.Name}}}
		}
		send(resp)
	}
}

func newPart2Client(t *testing.T, p *part2Server) *Client {
	t.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	go p.serve(serverIn, serverOut)
	t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })
	c := NewClient(clientIn, clientOut)
	if _, err := c.Initialize(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCallToolProgressInterleavedWithServerRequest(t *testing.T) {
	p := &part2Server{}
	c := newPart2Client(t, p)
	var mu sync.Mutex
	var got []core.ToolProgress
	ctx := core.WithToolProgress(testCtx(t), func(pr core.ToolProgress) { mu.Lock(); got = append(got, pr); mu.Unlock() })
	res, err := c.CallTool(ctx, "look", nil)
	if err != nil || res.Text != "ok:look" {
		t.Fatalf("call = %+v %v", res, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[1].Progress != 2 || got[1].Total != 4 || got[1].Message != "step" {
		t.Fatalf("progress = %+v", got)
	}
	if core.FormatToolProgress(got[1]) != "50% · step" {
		t.Fatalf("label = %q", core.FormatToolProgress(got[1]))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(string(p.calls[0]), `"progressToken":"supercli-`) {
		t.Fatalf("no progressToken: %s", p.calls[0])
	}
}

func TestCallToolCancelSendsNotificationsCancelled(t *testing.T) {
	p := &part2Server{block: make(chan struct{})}
	defer close(p.block)
	c := newPart2Client(t, p)
	ctx, cancel := context.WithCancel(testCtx(t))
	done := make(chan error, 1)
	go func() { _, err := c.CallTool(ctx, "slow", nil); done <- err }()
	waitLeaseCondition(t, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) == 1 })
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled call returned nil error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("call did not stop waiting after cancel")
	}
	waitLeaseCondition(t, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.cancelled) == 1 })
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(string(p.cancelled[0]), `"requestId":2`) { // 1 = initialize
		t.Fatalf("cancelled params = %s", p.cancelled[0])
	}
	if !p.pong {
		t.Fatal("server ping was not answered")
	}
}

func TestReadOnlyAnnotationsAreConservative(t *testing.T) {
	p := &part2Server{}
	c := newPart2Client(t, p)
	defs, err := c.ListTools(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Name: "srv", client: c, tools: defs}
	for name, want := range map[string]bool{"look": true, "wipe": false, "plain": false, "missing": false} {
		if got := s.ToolReadOnly(name); got != want {
			t.Fatalf("%s read-only = %v, want %v", name, got, want)
		}
	}
	// Manifest read_only overrides annotations in both directions.
	s.Config.ReadOnlyTools = map[string]bool{"plain": true, "look": false}
	if !s.ToolReadOnly("plain") || s.ToolReadOnly("look") {
		t.Fatal("manifest read_only not honoured")
	}
	reg := tools.NewRegistry()
	s.Config.ReadOnlyTools = nil
	RegisterTools(&Manager{servers: map[string]*Server{"srv": s}}, reg)
	for name, want := range map[string]bool{"mcp_srv_look": true, "mcp_srv_wipe": false, "mcp_srv_plain": false} {
		tool, ok := reg.Get(name)
		if !ok || tool.ReadOnly != want {
			t.Fatalf("%s registered ReadOnly=%v ok=%v", name, tool.ReadOnly, ok)
		}
	}
}

func TestSessionConversationPassesSessionMeta(t *testing.T) {
	p := &part2Server{}
	c := newPart2Client(t, p)
	s := &Server{Name: "srv", client: c, Config: ServerConfig{Session: SessionConversation}}
	ctx := llm.WithOpenCodeSession(testCtx(t), "ses_conv_1")
	if _, err := s.CallTool(ctx, "look", nil); err != nil {
		t.Fatal(err)
	}
	plain := &Server{Name: "plain", client: c}
	if _, err := plain.CallTool(ctx, "look", nil); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(string(p.calls[0]), `"supercli/session_id":"ses_conv_1"`) {
		t.Fatalf("session meta missing: %s", p.calls[0])
	}
	if strings.Contains(string(p.calls[1]), "supercli/session_id") {
		t.Fatalf("plain server got session meta: %s", p.calls[1])
	}
}

func writeManifestPackage(t *testing.T, manifestBody string) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "supercli-data")
	pkg := filepath.Join(data, PortableDirName, "decl")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := "server"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(filepath.Join(pkg, exe), []byte("placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "schema = 1\nname = \"decl\"\ncommand = \"" + exe + "\"\n" + manifestBody
	if err := os.WriteFile(filepath.Join(pkg, ManifestName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

const declaredManifest = `session = "conversation"

[[tools]]
name = "look"
description = "Look at the scene"
input_schema = '{"type":"object","properties":{"q":{"type":"string"}}}'
read_only = true
image_mode = "preview"

[[tools]]
name = "build"
description = "Build something"
[tools.input_schema]
type = "object"
[tools.input_schema.properties.size]
type = "integer"
`

func TestManifestDeclaredToolsParse(t *testing.T) {
	data := writeManifestPackage(t, declaredManifest)
	configs, packages, err := LoadWorkspace(data, nil)
	if err != nil || len(packages) != 1 || !packages[0].Available {
		t.Fatalf("packages = %+v err=%v", packages, err)
	}
	cfg := configs["decl"]
	if cfg.Session != SessionConversation || len(cfg.DeclaredTools) != 2 {
		t.Fatalf("config = %+v", cfg)
	}
	look, build := cfg.DeclaredTools[0], cfg.DeclaredTools[1]
	if !strings.Contains(string(look.InputSchema), `"q"`) || toolImageModeHint(look) != ImageModePreview || !cfg.ReadOnlyTools["look"] {
		t.Fatalf("look = %+v %s", look, look.InputSchema)
	}
	if !strings.Contains(string(build.InputSchema), `"size":{"type":"integer"}`) {
		t.Fatalf("table schema = %s", build.InputSchema)
	}
	if _, ok := cfg.ReadOnlyTools["build"]; ok {
		t.Fatal("undeclared read_only must stay unknown")
	}
	bad := writeManifestPackage(t, "[[tools]]\nname = \"x\"\ninput_schema = '[1,2]'\n")
	_, packages, _ = LoadWorkspace(bad, nil)
	if packages[0].Available || !strings.Contains(packages[0].Error, "input_schema") {
		t.Fatalf("bad schema accepted: %+v", packages[0])
	}
	badSession := writeManifestPackage(t, "session = \"global\"\n")
	_, packages, _ = LoadWorkspace(badSession, nil)
	if packages[0].Available {
		t.Fatalf("bad session accepted: %+v", packages[0])
	}
}

// Declared tools are searchable without a process (the placeholder command
// would fail to start); the real list wins once a client is attached.
func TestBridgeSearchUsesDeclaredToolsWithoutStarting(t *testing.T) {
	data := writeManifestPackage(t, declaredManifest)
	configs, _, err := LoadWorkspace(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(configs)
	bridge := NewBridge(m)
	res, _ := bridge.Spec().Fn(testCtx(t), json.RawMessage(`{"action":"search","server":"decl"}`))
	if res.Err != nil || !strings.Contains(res.Text, `"name":"look"`) || !strings.Contains(res.Text, `"declared":true`) ||
		!strings.Contains(res.Text, `"read_only":true`) || strings.Contains(res.Text, "errors") {
		t.Fatalf("search = %+v", res)
	}
	s, _ := m.Get("decl")
	if s.running() || s.Status().Running || s.Status().Tools != 2 {
		t.Fatalf("search started the process or lost tools: %+v", s.Status())
	}
	// A real tools/list replaces the declaration.
	p := &part2Server{}
	c := newPart2Client(t, p)
	real, err := c.ListTools(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.cmd, s.client, s.tools = &exec.Cmd{}, c, real
	s.mu.Unlock()
	res, _ = bridge.Spec().Fn(testCtx(t), json.RawMessage(`{"action":"search","server":"decl"}`))
	if !strings.Contains(res.Text, `"name":"wipe"`) || strings.Contains(res.Text, `"name":"build"`) || strings.Contains(res.Text, `"declared":true`) {
		t.Fatalf("real list did not win: %s", res.Text)
	}
	// Manifest image_mode hint still applies to a real tool lacking its own.
	if got := s.imageModeFor("look", ""); got != ImageModePreview {
		t.Fatalf("declared image hint lost: %q", got)
	}
	call, _ := bridge.Spec().Fn(testCtx(t), json.RawMessage(`{"action":"call","server":"decl","tool":"look","arguments":{}}`))
	if call.Err != nil || call.Text != "ok:look" {
		t.Fatalf("call = %+v", call)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// No conversation id in ctx: session servers send no session meta.
	if strings.Contains(string(p.calls[0]), "supercli/session_id") {
		t.Fatalf("unexpected session meta without conversation id: %s", p.calls[0])
	}
}

// A declared-only server without declarations keeps the old behaviour: search
// tries to start the process and reports its failure.
func TestBridgeSearchPlainServerStillStarts(t *testing.T) {
	m := NewManager(map[string]ServerConfig{"plain": {Command: filepath.Join(t.TempDir(), "missing-binary")}})
	res, _ := NewBridge(m).Spec().Fn(testCtx(t), json.RawMessage(`{"action":"search","server":"plain"}`))
	if !strings.Contains(res.Text, "errors") {
		t.Fatalf("plain server search should attempt start: %s", res.Text)
	}
}
