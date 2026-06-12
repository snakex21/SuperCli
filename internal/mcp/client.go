// Package mcp implements a minimal Model Context Protocol client
// for SuperCli: a JSON-RPC 2.0 transport over newline-delimited
// stdio, the initialize handshake, tools/list and tools/call.
//
// Reference: https://modelcontextprotocol.io/specification/2025-06-18
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// ProtocolVersion is the MCP protocol version this client speaks.
const ProtocolVersion = "2025-06-18"

// ToolDef is the static description of a tool exposed by an MCP
// server, as returned by tools/list.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Result is what tools/call returns: the concatenated text content
// plus the server's isError flag.
type Result struct {
	Text    string
	IsError bool
}

// ProtocolError is a JSON-RPC error returned by the server.
type ProtocolError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("mcp: protocol error %d: %s", e.Code, e.Message)
}

// Client speaks JSON-RPC 2.0 over a line-delimited byte stream. It
// is transport-agnostic: StdioServer wires it to a subprocess, tests
// wire it to in-process pipes.
type Client struct {
	w io.Writer

	writeMu sync.Mutex
	rpcID   atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan json.RawMessage
	closed    bool
}

// NewClient starts a client reading responses from r and writing
// requests to w. The read loop runs until r is exhausted or fails.
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		w:       w,
		pending: make(map[int64]chan json.RawMessage),
	}
	go c.readLoop(r)
	return c
}

// Initialize performs the MCP handshake: an initialize request
// followed by the notifications/initialized notification.
func (c *Client) Initialize(ctx context.Context) (json.RawMessage, error) {
	res, err := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "supercli",
			"version": "1.0",
		},
	})
	if err != nil {
		return nil, err
	}
	if err := c.Notify("notifications/initialized", map[string]any{}); err != nil {
		return nil, err
	}
	return res, nil
}

// ListTools sends tools/list and decodes the tool definitions.
// Pagination cursors are followed until exhausted.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	var out []ToolDef
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.Call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []ToolDef `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp: tools/list decode: %w", err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// CallTool invokes a tool by its server-side name with raw JSON args.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return Result{}, err
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Result{}, fmt.Errorf("mcp: tools/call decode: %w", err)
	}
	var text string
	for _, part := range parsed.Content {
		if part.Type == "text" {
			text += part.Text
		}
	}
	return Result{Text: text, IsError: parsed.IsError}, nil
}

// Call sends a JSON-RPC request and blocks until the matching
// response arrives or ctx is done.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.rpcID.Add(1)
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}

	respCh := make(chan json.RawMessage, 1)
	c.pendingMu.Lock()
	if c.closed {
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("mcp: client closed")
	}
	c.pending[id] = respCh
	c.pendingMu.Unlock()

	if err := c.writeLine(payload); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("mcp: %s: write: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case raw, ok := <-respCh:
		if !ok || raw == nil {
			return nil, fmt.Errorf("mcp: %s: connection closed before response", method)
		}
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  *ProtocolError  `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("mcp: %s: decode: %w", method, err)
		}
		if envelope.Error != nil {
			return nil, envelope.Error
		}
		return envelope.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no id, no response).
func (c *Client) Notify(method string, params any) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	return c.writeLine(payload)
}

func (c *Client) writeLine(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.w.Write(append(payload, '\n'))
	return err
}

// readLoop dispatches incoming responses to pending callers. When the
// reader ends, every pending caller is unblocked with a closed channel.
func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			ID *int64 `json:"id"`
		}
		if err := json.Unmarshal(line, &env); err != nil || env.ID == nil {
			continue // notification or garbage — skip
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[*env.ID]
		if ok {
			delete(c.pending, *env.ID)
		}
		c.pendingMu.Unlock()
		if ok {
			raw := make(json.RawMessage, len(line))
			copy(raw, line)
			ch <- raw
			close(ch)
		}
	}
	// Reader ended: fail all pending calls.
	c.pendingMu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.pendingMu.Unlock()
}
