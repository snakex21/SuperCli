package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools"
)

// fakeServer reads JSON-RPC requests line-by-line from in and writes
// canned MCP responses to out. It implements initialize, tools/list
// (two pages), and tools/call (echo).
func fakeServer(t *testing.T, in io.Reader, out io.Writer) {
	t.Helper()
	sc := bufio.NewScanner(in)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue // notification (notifications/initialized)
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID}
		switch req.Method {
		case "initialize":
			resp["result"] = map[string]any{
				"protocolVersion": ProtocolVersion,
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			}
		case "tools/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Cursor == "" {
				resp["result"] = map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "echoes its input",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
					}},
					"nextCursor": "page2",
				}
			} else {
				resp["result"] = map[string]any{
					"tools": []map[string]any{{
						"name":        "fail",
						"description": "always errors",
						"inputSchema": map[string]any{"type": "object"},
					}},
				}
			}
		case "tools/call":
			var p struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Name == "fail" {
				resp["result"] = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "boom"}},
					"isError": true,
				}
			} else {
				resp["result"] = map[string]any{
					"content": []map[string]any{{"type": "text", "text": "echo:" + string(p.Args)}},
				}
			}
		default:
			resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	clientIn, serverOut := io.Pipe() // server writes -> client reads
	serverIn, clientOut := io.Pipe() // client writes -> server reads
	go fakeServer(t, serverIn, serverOut)
	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = serverOut.Close()
	})
	return NewClient(clientIn, clientOut)
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestClient_InitializeAndListTools(t *testing.T) {
	c := newTestClient(t)
	ctx := testCtx(t)
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tds, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tds) != 2 || tds[0].Name != "echo" || tds[1].Name != "fail" {
		t.Fatalf("tools = %+v, want echo+fail (pagination followed)", tds)
	}
	if !strings.Contains(string(tds[0].InputSchema), `"text"`) {
		t.Errorf("echo schema = %s", tds[0].InputSchema)
	}
}

func TestClient_CallTool(t *testing.T) {
	c := newTestClient(t)
	ctx := testCtx(t)
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError || !strings.Contains(res.Text, `"text":"hi"`) {
		t.Errorf("res = %+v", res)
	}
	res, err = c.CallTool(ctx, "fail", nil)
	if err != nil {
		t.Fatalf("CallTool(fail): %v", err)
	}
	if !res.IsError || res.Text != "boom" {
		t.Errorf("fail res = %+v, want isError boom", res)
	}
}

func TestClient_UnknownMethod(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Call(testCtx(t), "nope/nope", map[string]any{})
	var perr *ProtocolError
	if err == nil {
		t.Fatal("want protocol error")
	}
	if ok := errorAs(err, &perr); !ok || perr.Code != -32601 {
		t.Errorf("err = %v", err)
	}
}

func errorAs(err error, target **ProtocolError) bool {
	p, ok := err.(*ProtocolError)
	if ok {
		*target = p
	}
	return ok
}

func TestRegisterTools_PrefixAndProxy(t *testing.T) {
	// Server with an injected client (no subprocess).
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	go fakeServer(t, serverIn, serverOut)
	t.Cleanup(func() { _ = clientOut.Close(); _ = serverOut.Close() })
	c := NewClient(clientIn, clientOut)
	ctx := testCtx(t)
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tds, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	s := &Server{Name: "fake"}
	s.client = c
	s.tools = tds

	m := &Manager{servers: map[string]*Server{"fake": s}}
	reg := tools.NewRegistry()
	n := RegisterTools(m, reg)
	if n != 2 {
		t.Fatalf("registered = %d, want 2", n)
	}
	tool, ok := reg.Get("mcp_fake_echo")
	if !ok {
		t.Fatalf("mcp_fake_echo not registered; names: %v", reg.Names())
	}
	// MCP tools must be deferred: not visible by default.
	if len(reg.Visible()) != 0 {
		t.Errorf("MCP tools should not be always-on; visible: %v", reg.Visible())
	}
	res, err := tool.Fn(ctx, json.RawMessage(`{"text":"x"}`))
	if err != nil {
		t.Fatalf("Fn: %v", err)
	}
	if res.Err != nil || !strings.Contains(res.Text, "echo:") {
		t.Errorf("res = %+v", res)
	}
	// isError surfaces as Result.Err.
	failTool, _ := reg.Get("mcp_fake_fail")
	res, _ = failTool.Fn(ctx, nil)
	if res.Err == nil {
		t.Error("fail tool should set Result.Err")
	}
}
