package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// StdioServer is an MCP server that runs as a subprocess
// speaking JSON-RPC over its stdin/stdout. It implements
// Server.
type StdioServer struct {
	name    string
	command string
	args    []string
	env     []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	rpcID   atomic.Int64
	pending map[int64]chan<- *json.RawMessage
	closed  chan struct{}
}

// NewStdioServer returns a server that will be launched on the
// first call to Start. The first Start spawns the subprocess;
// subsequent Starts are no-ops.
func NewStdioServer(name, command string, args ...string) *StdioServer {
	return &StdioServer{
		name:    name,
		command: command,
		args:    args,
		pending: make(map[int64]chan<- *json.RawMessage),
		closed:  make(chan struct{}),
	}
}

// WithEnv sets additional environment variables for the
// subprocess. Must be called before Start.
func (s *StdioServer) WithEnv(env ...string) *StdioServer {
	s.env = append(s.env, env...)
	return s
}

// Name implements Server.
func (s *StdioServer) Name() string { return s.name }

// Start launches the subprocess and performs the MCP
// "initialize" handshake. If the server is already started,
// Start is a no-op.
func (s *StdioServer) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, s.command, s.args...)
	cmd.Env = append(cmd.Environ(), s.env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp.StdioServer.Start: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp.StdioServer.Start: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp.StdioServer.Start: %w", err)
	}
	s.cmd = cmd
	s.stdin = stdin
	s.stdout = bufio.NewScanner(stdout)
	s.stdout.Buffer(make([]byte, 64*1024), 1024*1024)
	go s.readLoop()
	return nil
}

// Stop kills the subprocess and closes the channels.
func (s *StdioServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return nil
	}
	select {
	case <-s.closed:
		// already closed
	default:
		close(s.closed)
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	err := s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	s.cmd = nil
	return err
}

// Tools returns an empty list. Subclasses should override.
func (s *StdioServer) Tools() []ToolDef { return nil }

// CallTool implements Server by sending a "tools/call"
// request and waiting for the response. It assumes the server
// has already been initialized via Start.
func (s *StdioServer) CallTool(ctx context.Context, name string, args []byte) (Result, error) {
	params := map[string]any{
		"name":      name,
		"arguments": json.RawMessage(args),
	}
	resp, err := s.call(ctx, "tools/call", params)
	if err != nil {
		return Result{}, err
	}
	// Minimal decode: look for "content" array with type=text.
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return Result{}, fmt.Errorf("mcp.StdioServer.CallTool: decode: %w", err)
	}
	var text string
	for _, c := range parsed.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	return Result{Text: text, IsError: parsed.IsError}, nil
}

// Initialize sends the MCP "initialize" request and returns the
// server's response payload. Public so subclasses (and tests)
// can inspect capabilities.
func (s *StdioServer) Initialize(ctx context.Context) ([]byte, error) {
	params := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "supercli",
			"version": "0.2.0-f2",
		},
	}
	return s.call(ctx, "initialize", params)
}

// call sends a JSON-RPC request and blocks for the matching
// response.
func (s *StdioServer) call(ctx context.Context, method string, params any) ([]byte, error) {
	id := s.rpcID.Add(1)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	respCh := make(chan *json.RawMessage, 1)
	s.mu.Lock()
	s.pending[id] = respCh
	s.mu.Unlock()

	s.mu.Lock()
	_, err = s.stdin.Write(payload)
	s.mu.Unlock()
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("mcp.StdioServer.call(%s): write: %w", method, err)
	}

	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case raw := <-respCh:
		if raw == nil {
			return nil, fmt.Errorf("mcp.StdioServer.call(%s): no response", method)
		}
		// Decode to extract "result" or "error".
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  *ProtocolError   `json:"error"`
		}
		if err := json.Unmarshal(*raw, &envelope); err != nil {
			return nil, fmt.Errorf("mcp.StdioServer.call(%s): decode: %w", method, err)
		}
		if envelope.Error != nil {
			return nil, envelope.Error
		}
		return envelope.Result, nil
	}
}

// readLoop runs in a goroutine and dispatches incoming JSON-RPC
// responses to waiting callers.
func (s *StdioServer) readLoop() {
	for {
		if !s.stdout.Scan() {
			return
		}
		line := s.stdout.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *ProtocolError  `json:"error"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			// skip garbage
			continue
		}
		if env.ID == 0 {
			// notification, ignore for now
			continue
		}
		s.mu.Lock()
		ch, ok := s.pending[env.ID]
		if ok {
			delete(s.pending, env.ID)
		}
		s.mu.Unlock()
		if ok {
			ch <- &env.Result
			close(ch)
		}
	}
}
