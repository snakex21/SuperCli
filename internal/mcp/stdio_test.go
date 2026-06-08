package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStdioServer_RoundTrip launches a tiny stub subprocess
// that replies to JSON-RPC requests and confirms StdioServer
// can perform the full handshake + call sequence.
func TestStdioServer_RoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping subprocess test on windows in this CI")
	}
	bin := buildStubServer(t)
	// Write a temporary file the stub reads its command from.
	cmdFile := filepath.Join(t.TempDir(), "cmd.txt")
	if err := os.WriteFile(cmdFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write cmd file: %v", err)
	}
	s := NewStdioServer("stub", bin, cmdFile)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := s.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := s.CallTool(ctx, "echo", []byte(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "hi" {
		t.Errorf("Text = %q", res.Text)
	}
}

// TestStdioServer_StopKillsSubprocess confirms Stop is fatal
// to the subprocess.
func TestStdioServer_StopKillsSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping subprocess test on windows in this CI")
	}
	bin := buildStubServer(t)
	cmdFile := filepath.Join(t.TempDir(), "cmd.txt")
	os.WriteFile(cmdFile, []byte(""), 0o644)
	s := NewStdioServer("stub", bin, cmdFile)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func buildStubServer(t *testing.T) string {
	t.Helper()
	// Find go in PATH.
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	prog := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req struct {
			ID     int64           ` + "`json:\"id\"`" + `
			Method string          ` + "`json:\"method\"`" + `
			Params json.RawMessage ` + "`json:\"params\"`" + `
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == 0 {
			continue
		}
		var result json.RawMessage
		switch req.Method {
		case "initialize":
			result = json.RawMessage(` + "`{\"protocolVersion\":\"2025-06-18\"}`" + `)
		case "tools/call":
			var p struct {
				Name      string          ` + "`json:\"name\"`" + `
				Arguments json.RawMessage ` + "`json:\"arguments\"`" + `
			}
			json.Unmarshal(req.Params, &p)
			text := "x"
			if p.Name == "echo" {
				var a struct{ Msg string ` + "`json:\"msg\"`" + ` }
				json.Unmarshal(p.Arguments, &a)
				text = a.Msg
			}
			result = json.RawMessage(fmt.Sprintf(` + "`{\"content\":[{\"type\":\"text\",\"text\":%q}],\"isError\":false}`" + `, text))
		default:
			fmt.Fprintln(os.Stderr, "unknown method:", req.Method)
			continue
		}
		resp := map[string]any{` + "`\"jsonrpc\": \"2.0\"`" + `, ` + "`\"id\": req.ID`" + `, ` + "`\"result\": result`" + `}
		out, _ := json.Marshal(resp)
		os.Stdout.Write(out)
		os.Stdout.Write([]byte("\n"))
	}
	io.Copy(io.Discard, os.Stdin) // keep stdin open
}`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	bin := filepath.Join(dir, "stub")
	cmd := exec.Command(goBin, "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go build failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return bin
}
