// Package mcp implements a minimal Model Context Protocol client
// for SuperCli: a JSON-RPC 2.0 transport over newline-delimited
// stdio, the initialize handshake, tools/list and tools/call.
//
// Reference: https://modelcontextprotocol.io/specification/2025-06-18
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"supercli/internal/tools/core"
)

// ProtocolVersion is the MCP protocol version this client speaks.
const ProtocolVersion = "2025-06-18"

// ToolDef is the static description of a tool exposed by an MCP
// server, as returned by tools/list.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Meta is the optional MCP `_meta` object; SuperCli reads only keys
	// prefixed "supercli/" (see MetaImageModeKey).
	Meta json.RawMessage `json:"_meta,omitempty"`
	// Annotations are the standard MCP behaviour hints (untrusted hints).
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations mirrors the MCP tool annotations SuperCli consults.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
}

// ReadOnly reports whether the tool is declared read-only and not
// destructive. Missing annotations are unknown, i.e. not read-only.
func (td ToolDef) ReadOnly() bool {
	a := td.Annotations
	if a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint {
		return false
	}
	return a.DestructiveHint == nil || !*a.DestructiveHint
}

// Result is what tools/call returns: the concatenated text content
// plus the server's isError flag.
type Result struct {
	Text    string
	IsError bool
	Images  []*core.ImageContent
	// Files holds binary non-image content (embedded resource blobs, audio).
	Files []*core.ImageContent
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

	progressSeq atomic.Int64
	progressMu  sync.Mutex
	progress    map[string]func(core.ToolProgress)
}

// NewClient starts a client reading responses from r and writing
// requests to w. The read loop runs until r is exhausted or fails.
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		w:        w,
		pending:  make(map[int64]chan json.RawMessage),
		progress: make(map[string]func(core.ToolProgress)),
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
	return c.CallToolMeta(ctx, name, args, nil)
}

// CallToolMeta is CallTool with extra `_meta` keys (SuperCli uses only the
// "supercli/" prefix). A standard `_meta.progressToken` is always sent;
// notifications/progress for it are forwarded to core.ReportToolProgress(ctx).
func (c *Client) CallToolMeta(ctx context.Context, name string, args json.RawMessage, meta map[string]any) (Result, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	token := fmt.Sprintf("supercli-%d", c.progressSeq.Add(1))
	fullMeta := map[string]any{"progressToken": token}
	for k, v := range meta {
		if k != "progressToken" {
			fullMeta[k] = v
		}
	}
	c.progressMu.Lock()
	c.progress[token] = func(p core.ToolProgress) { core.ReportToolProgress(ctx, p) }
	c.progressMu.Unlock()
	defer func() {
		c.progressMu.Lock()
		delete(c.progress, token)
		c.progressMu.Unlock()
	}()
	raw, err := c.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
		"_meta":     fullMeta,
	})
	if err != nil {
		return Result{}, err
	}
	return decodeToolResult(raw)
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
		_, stillPending := c.pending[id]
		delete(c.pending, id)
		c.pendingMu.Unlock()
		// MCP cancellation: tell the server to stop work for this request.
		// initialize must never be cancelled per the specification.
		if stillPending && method != "initialize" {
			_ = c.Notify("notifications/cancelled", map[string]any{"requestId": id, "reason": "cancelled by user"})
		}
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
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			continue // garbage — skip
		}
		if env.Method != "" {
			// Server notification or server->client request, interleaved
			// with responses. Never route these to a pending caller.
			if len(env.ID) == 0 || string(env.ID) == "null" {
				c.handleNotification(env.Method, env.Params)
			} else {
				c.answerServerRequest(env.ID, env.Method)
			}
			continue
		}
		var id int64
		if len(env.ID) == 0 || json.Unmarshal(env.ID, &id) != nil {
			continue
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
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

// handleNotification dispatches server notifications. Only
// notifications/progress is consumed; everything else is ignored.
func (c *Client) handleNotification(method string, params json.RawMessage) {
	if method != "notifications/progress" {
		return
	}
	var p struct {
		Token    json.RawMessage `json:"progressToken"`
		Progress float64         `json:"progress"`
		Total    float64         `json:"total"`
		Message  string          `json:"message"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var token string
	if json.Unmarshal(p.Token, &token) != nil {
		return
	}
	c.progressMu.Lock()
	fn := c.progress[token]
	c.progressMu.Unlock()
	if fn != nil {
		fn(core.ToolProgress{Progress: p.Progress, Total: p.Total, Message: p.Message})
	}
}

// answerServerRequest replies to server->client requests so a server never
// blocks waiting: ping succeeds, anything else is "method not found".
func (c *Client) answerServerRequest(id json.RawMessage, method string) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if method == "ping" {
		resp["result"] = map[string]any{}
	} else {
		resp["error"] = map[string]any{"code": -32601, "message": "method not supported by client: " + method}
	}
	if payload, err := json.Marshal(resp); err == nil {
		go func() { _ = c.writeLine(payload) }()
	}
}

// Preserve native image blocks: never turn binary content into model text.
// Bounds are within the transport's 8 MiB frame limit, and apply in aggregate.
const maxMCPImageBytes = 5 << 20
const maxMCPImages = 16

// decodeBinaryPart stores one embedded binary payload as an image (if it is a
// supported, consistently labelled image) or a file. Budget overflow or bad
// base64 becomes a text note instead of failing the entire result.
func decodeBinaryPart(result *Result, text *strings.Builder, total *int, uri, declared, blob string) {
	if blob == "" {
		return
	}
	if len(result.Images)+len(result.Files) >= maxMCPImages || len(blob) > base64.StdEncoding.EncodedLen(maxMCPImageBytes-*total) {
		fmt.Fprintf(text, "\n[binary resource %s omitted: exceeds bounded media budget]\n", uri)
		return
	}
	data, err := base64.StdEncoding.DecodeString(blob)
	if err != nil || len(data) == 0 {
		fmt.Fprintf(text, "\n[binary resource %s omitted: invalid base64]\n", uri)
		return
	}
	*total += len(data)
	declared = strings.ToLower(strings.TrimSpace(declared))
	detected := http.DetectContentType(data)
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		if declared == "" || declared == detected {
			result.Images = append(result.Images, &core.ImageContent{MediaType: detected, Data: data, Name: uri})
			return
		}
	}
	if declared == "" {
		declared = detected
	}
	result.Files = append(result.Files, &core.ImageContent{MediaType: declared, Data: data, Name: uri})
}

func decodeToolResult(raw json.RawMessage) (Result, error) {
	var parsed struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MIME     string `json:"mimeType"`
			URI      string `json:"uri"`
			Name     string `json:"name"`
			Resource *struct {
				URI  string  `json:"uri"`
				MIME string  `json:"mimeType"`
				Text *string `json:"text"`
				Blob string  `json:"blob"`
			} `json:"resource"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Result{}, fmt.Errorf("mcp: tools/call decode: %w", err)
	}
	result := Result{IsError: parsed.IsError}
	var text strings.Builder
	total := 0
	for _, part := range parsed.Content {
		switch part.Type {
		case "text":
			text.WriteString(part.Text)
		case "image":
			if len(result.Images) >= maxMCPImages || len(part.Data) > base64.StdEncoding.EncodedLen(maxMCPImageBytes-total) {
				return Result{}, fmt.Errorf("mcp: image result exceeds bounded image budget")
			}
			data, err := base64.StdEncoding.DecodeString(part.Data)
			if err != nil {
				return Result{}, fmt.Errorf("mcp: invalid image base64: %w", err)
			}
			mime := http.DetectContentType(data)
			switch mime {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
			default:
				return Result{}, fmt.Errorf("mcp: unsupported image type %q", mime)
			}
			if mime != strings.ToLower(strings.TrimSpace(part.MIME)) {
				return Result{}, fmt.Errorf("mcp: image MIME mismatch")
			}
			total += len(data)
			if total > maxMCPImageBytes {
				return Result{}, fmt.Errorf("mcp: image result exceeds bounded image budget")
			}
			result.Images = append(result.Images, &core.ImageContent{MediaType: mime, Data: data})
		case "resource_link":
			label := strings.TrimSpace(part.Name)
			if label == "" {
				label = part.URI
			}
			fmt.Fprintf(&text, "\n[resource link: %s <%s>", label, part.URI)
			if part.MIME != "" {
				fmt.Fprintf(&text, " %s", part.MIME)
			}
			text.WriteString("]\n")
		case "resource":
			if part.Resource == nil {
				continue
			}
			res := part.Resource
			if res.Text != nil {
				fmt.Fprintf(&text, "\n[resource %s]\n%s\n", res.URI, *res.Text)
				continue
			}
			decodeBinaryPart(&result, &text, &total, res.URI, res.MIME, res.Blob)
		case "audio":
			decodeBinaryPart(&result, &text, &total, "audio", part.MIME, part.Data)
		}
	}
	// Preserve structured results unless an equivalent JSON value is already
	// present in the text, including a backwards-compatible text block beside
	// a human-readable summary. Ordinary prose is not a substitute for data.
	if len(parsed.Structured) > 0 && string(parsed.Structured) != "null" {
		duplicate := false
		if text.Len() > 0 {
			var structured any
			decoder := json.NewDecoder(bytes.NewReader(parsed.Structured))
			decoder.UseNumber()
			if err := decoder.Decode(&structured); err != nil {
				return Result{}, fmt.Errorf("mcp: structured result decode: %w", err)
			}
			duplicate = jsonTextMatchesValue(text.String(), structured)
			if !duplicate && len(parsed.Content) > 1 {
				for _, part := range parsed.Content {
					if part.Type == "text" && jsonTextMatchesValue(part.Text, structured) {
						duplicate = true
						break
					}
				}
			}
		}
		if !duplicate {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.Write(parsed.Structured)
		}
	}
	result.Text = text.String()
	return result, nil
}

// Compare complete JSON values independent of whitespace and object key order.
// UseNumber keeps distinct large integers from collapsing through float64;
// differing number spellings are conservatively retained rather than discarded.
func jsonTextMatchesValue(text string, value any) bool {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false
	}
	return reflect.DeepEqual(decoded, value)
}
