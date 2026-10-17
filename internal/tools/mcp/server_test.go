package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServerMCPHelper speaks real line-delimited MCP JSON-RPC in a child of
// this test binary. Normal package tests neither read stdin nor exit here.
func TestServerMCPHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_TEST_MCP_SERVER_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	handshake, initialized := false, false
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		if len(req.ID) == 0 {
			if req.Method == "notifications/initialized" && handshake {
				initialized = true
			}
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		var result any
		var rpcErr *ProtocolError
		switch req.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.ProtocolVersion != ProtocolVersion {
				rpcErr = &ProtocolError{Code: -32602, Message: "invalid protocol version"}
				break
			}
			handshake = true
			result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "go-echo", "version": "1.0"}}
		case "tools/list", "tools/call":
			if !initialized {
				rpcErr = &ProtocolError{Code: -32000, Message: "initialize notification required"}
				break
			}
			if req.Method == "tools/list" {
				result = map[string]any{"tools": []ToolDef{{Name: "echo", Description: "echo text and read a fixture", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"path":{"type":"string"}},"required":["text","path"]}`)}}}
				break
			}
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Text string `json:"text"`
					Path string `json:"path"`
				} `json:"arguments"`
			}
			if json.Unmarshal(req.Params, &params) != nil || params.Name != "echo" {
				rpcErr = &ProtocolError{Code: -32602, Message: "unknown tool or invalid arguments"}
				break
			}
			content, err := os.ReadFile(params.Arguments.Path)
			text := "echo: " + params.Arguments.Text + "\n" + string(content)
			if err != nil {
				text = err.Error()
			}
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": err != nil}
		default:
			rpcErr = &ProtocolError{Code: -32601, Message: "method not found"}
		}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

// TestServer_RealSubprocess exercises the full subprocess path
// (spawn, initialize handshake, tools/list, tools/call, stop)
// against a real Go helper subprocess, including an actual fixture read.
func TestServer_RealSubprocess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	const fixture = "MCP fixture: Łódź\n"
	if err := os.WriteFile(filepath.Join(home, "fixture.txt"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Name: "goecho",
		Config: ServerConfig{
			Command: executable,
			Args:    []string{"-test.run=^TestServerMCPHelper$"},
			Env:     map[string]string{"SUPERCLI_TEST_MCP_SERVER_HELPER": "1"},
			Dir:     home,
		},
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
	res, err := s.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello mcp","path":"fixture.txt"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(res.Text, "echo: hello mcp") {
		t.Errorf("res = %+v", res)
	}
	if res.IsError || !strings.Contains(res.Text, fixture) {
		t.Fatalf("fixture was not read by the subprocess: %+v", res)
	}
	if err := os.WriteFile(filepath.Join(home, "fixture.txt"), []byte("updated fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err = s.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello mcp","path":"fixture.txt"}`))
	if err != nil || res.IsError || res.Text != "echo: hello mcp\nupdated fixture" {
		t.Fatalf("subprocess did not reread the actual file: %+v, %v", res, err)
	}
	res, err = s.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello mcp","path":"missing.txt"}`))
	if err != nil || !res.IsError {
		t.Fatalf("missing fixture must be a tool error: %+v, %v", res, err)
	}
	if err := s.Stop(); err != nil && !strings.Contains(err.Error(), "exit") {
		t.Logf("Stop: %v (process kill error is acceptable)", err)
	}
	if s.Status().Running {
		t.Error("server still running after Stop")
	}
}
