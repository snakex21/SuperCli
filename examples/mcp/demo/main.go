// Command demo is a tiny standalone MCP server for manually testing
// SuperCli's "MCP 2" features (images, progress, cancellation, large
// results, sessions, read-only annotations). Standard library only.
//
// Transport: JSON-RPC 2.0 over stdio, one JSON message per line
// (newline-delimited), protocol version 2025-06-18.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2025-06-18"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type server struct {
	out     io.Writer
	writeMu sync.Mutex

	mu       sync.Mutex
	running  map[string]context.CancelFunc // request id -> cancel
	sessions map[string]int                // session id -> call count
}

func main() {
	s := &server{out: os.Stdout, running: map[string]context.CancelFunc{}, sessions: map[string]int{}}
	s.serve(os.Stdin)
}

func (s *server) serve(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var wg sync.WaitGroup
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.send(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		if req.Method == "" {
			continue // a response to something we never send; ignore
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			s.notification(req)
			continue
		}
		if req.Method == "tools/call" {
			// Run concurrently so notifications/cancelled can reach it.
			ctx, cancel := context.WithCancel(context.Background())
			key := idKey(req.ID)
			s.mu.Lock()
			s.running[key] = cancel
			s.mu.Unlock()
			wg.Add(1)
			go func(req request) {
				defer wg.Done()
				defer func() {
					s.mu.Lock()
					delete(s.running, key)
					s.mu.Unlock()
					cancel()
				}()
				result, rpcErr := s.callTool(ctx, req.Params)
				if ctx.Err() != nil {
					return // cancelled: per MCP, send no response
				}
				s.reply(req.ID, result, rpcErr)
			}(req)
			continue
		}
		result, rpcErr := s.handle(req)
		s.reply(req.ID, result, rpcErr)
	}
	s.mu.Lock()
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	wg.Wait()
}

func idKey(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	return fmt.Sprint(v)
}

func (s *server) send(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *server) reply(id json.RawMessage, result any, e *rpcError) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	s.send(msg)
}

func (s *server) notification(req request) {
	switch req.Method {
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
			Reason    string          `json:"reason"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			return
		}
		s.mu.Lock()
		cancel := s.running[idKey(p.RequestID)]
		s.mu.Unlock()
		if cancel != nil {
			fmt.Fprintf(os.Stderr, "demo: cancelled request %s (%s)\n", idKey(p.RequestID), p.Reason)
			cancel()
		}
	case "notifications/initialized":
		// nothing to do
	}
}

func (s *server) handle(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "supercli-demo", "version": "1.0.0"},
			"instructions":    "Demo server for testing SuperCli MCP 2 features.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolList()}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

func boolPtr(v bool) *bool { return &v }

type annotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *annotations    `json:"annotations,omitempty"`
}

func toolList() []toolDef {
	ro := &annotations{ReadOnlyHint: boolPtr(true)}
	return []toolDef{
		{Name: "demo_image", Description: "Return a generated 320x200 PNG test image plus a short text.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), Annotations: ro},
		{Name: "demo_slow", Description: "Work for N seconds (default 5, max 30), reporting progress every second; honours cancellation.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"seconds":{"type":"integer","minimum":1,"maximum":30,"description":"Duration in seconds (default 5)"}}}`), Annotations: ro},
		{Name: "demo_big", Description: "Return about 200 KB of numbered text lines (tests large results / read_output).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), Annotations: ro},
		{Name: "demo_session", Description: "Return the supercli/session_id received in _meta and a per-session call counter.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), Annotations: ro},
		{Name: "demo_write", Description: "Pretend write: echoes text back. Marked destructive (no real side effects).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"Text to echo"}},"required":["text"]}`),
			Annotations: &annotations{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(true)}},
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      map[string]any  `json:"_meta"`
}

func textContent(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func toolResult(isError bool, content ...map[string]any) map[string]any {
	return map[string]any{"content": content, "isError": isError}
}

func (s *server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	switch p.Name {
	case "demo_image":
		data, err := renderPNG()
		if err != nil {
			return toolResult(true, textContent("render failed: "+err.Error())), nil
		}
		return toolResult(false,
			textContent("Demo image: 320x200 gradient with a checker pattern and a circle."),
			map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(data)},
		), nil
	case "demo_slow":
		var args struct {
			Seconds int `json:"seconds"`
		}
		_ = json.Unmarshal(p.Arguments, &args)
		n := args.Seconds
		if n <= 0 {
			n = 5
		}
		if n > 30 {
			n = 30
		}
		token := p.Meta["progressToken"]
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for i := 1; i <= n; i++ {
			select {
			case <-ctx.Done():
				return nil, nil
			case <-ticker.C:
			}
			if token != nil {
				s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{
					"progressToken": token, "progress": i, "total": n, "message": fmt.Sprintf("krok %d/%d", i, n),
				}})
			}
		}
		return toolResult(false, textContent(fmt.Sprintf("done (%d s)", n))), nil
	case "demo_big":
		var b strings.Builder
		for i := 1; b.Len() < 200*1024; i++ {
			fmt.Fprintf(&b, "line %06d: the quick brown fox jumps over the lazy dog\n", i)
		}
		return toolResult(false, textContent(b.String())), nil
	case "demo_session":
		id, _ := p.Meta["supercli/session_id"].(string)
		key := id
		if key == "" {
			key = "(none)"
		}
		s.mu.Lock()
		s.sessions[key]++
		count := s.sessions[key]
		s.mu.Unlock()
		return toolResult(false, textContent(fmt.Sprintf("session_id=%s calls=%d", key, count))), nil
	case "demo_write":
		var args struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(p.Arguments, &args)
		return toolResult(false, textContent("echo (nothing was written): "+args.Text)), nil
	default:
		return toolResult(true, textContent("unknown tool: "+p.Name)), nil
	}
}

func renderPNG() ([]byte, error) {
	const w, h = 320, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8(x * 255 / (w - 1))
			g := uint8(y * 255 / (h - 1))
			b := uint8(160)
			if ((x/20)+(y/20))%2 == 0 {
				b = 230
			}
			dx, dy := x-w/2, y-h/2
			if d := dx*dx + dy*dy; d < 60*60 && d > 52*52 {
				r, g, b = 255, 255, 255
			}
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
