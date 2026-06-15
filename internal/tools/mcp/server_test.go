package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// nodeEchoServer is a minimal real MCP server (no npm deps): line-
// delimited JSON-RPC over stdio with initialize, tools/list, tools/call.
const nodeEchoServer = `
const rl = require('readline').createInterface({ input: process.stdin });
rl.on('line', (line) => {
  let req; try { req = JSON.parse(line); } catch { return; }
  if (req.id === undefined) return; // notification
  const resp = { jsonrpc: '2.0', id: req.id };
  if (req.method === 'initialize') {
    resp.result = { protocolVersion: '2025-06-18', capabilities: {}, serverInfo: { name: 'node-echo', version: '1.0' } };
  } else if (req.method === 'tools/list') {
    resp.result = { tools: [{ name: 'echo', description: 'echo text back', inputSchema: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] } }] };
  } else if (req.method === 'tools/call') {
    resp.result = { content: [{ type: 'text', text: 'echo: ' + (req.params.arguments.text || '') }] };
  } else {
    resp.error = { code: -32601, message: 'method not found' };
  }
  process.stdout.write(JSON.stringify(resp) + '\n');
});
`

// TestServer_RealSubprocess exercises the full subprocess path
// (spawn, initialize handshake, tools/list, tools/call, stop)
// against a real Node-based MCP server. Skipped when node is absent.
func TestServer_RealSubprocess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	s := &Server{
		Name:   "nodeecho",
		Config: ServerConfig{Command: "node", Args: []string{"-e", nodeEchoServer}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	st := s.Status()
	if !st.Running || st.Tools != 1 {
		t.Fatalf("status = %+v", st)
	}
	res, err := s.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello mcp"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(res.Text, "echo: hello mcp") {
		t.Errorf("res = %+v", res)
	}
	if err := s.Stop(); err != nil && !strings.Contains(err.Error(), "exit") {
		t.Logf("Stop: %v (process kill error is acceptable)", err)
	}
	if s.Status().Running {
		t.Error("server still running after Stop")
	}
}
