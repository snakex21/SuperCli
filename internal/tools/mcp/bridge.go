package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"supercli/internal/tools/core"
)

const bridgeResultHead = 16 << 10
const bridgeResultTail = 4 << 10

// Bridge is the single thin model-facing entry point for every configured MCP
// server. It keeps the default tool catalog stable: servers are listed locally
// and started only when the model searches or calls one of them.
type Bridge struct {
	manager *Manager
}

type bridgeRequest struct {
	Action    string          `json:"action"`
	Server    string          `json:"server"`
	Query     string          `json:"query"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Calls     []bridgeCall    `json:"calls"`
}

// bridgeCall is one item of a batched action=call request.
type bridgeCall struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// maxBridgeBatch bounds one batched call; items run sequentially in order.
const maxBridgeBatch = 8

func NewBridge(manager *Manager) *Bridge { return &Bridge{manager: manager} }

func (b *Bridge) Spec() core.Tool {
	return core.Tool{
		Name:        "mcp_bridge",
		Description: "Access configured MCP capabilities, including web/browser tools. list is local; search starts a matching server lazily; call invokes one tool, or up to 8 tools in order via calls=[{server,tool,arguments}] (an item error does not stop the rest).",
		Schema:      `{"type":"object","properties":{"action":{"type":"string","enum":["list","search","call"]},"server":{"type":"string","description":"MCP server name"},"query":{"type":"string","description":"Tool/server capability to find"},"tool":{"type":"string","description":"Exact remote MCP tool name"},"arguments":{"type":"object","additionalProperties":true},"calls":{"type":"array","maxItems":8,"description":"Batch for action=call, executed sequentially","items":{"type":"object","properties":{"server":{"type":"string"},"tool":{"type":"string"},"arguments":{"type":"object","additionalProperties":true}},"required":["server","tool"]}}},"required":["action"]}`,
		Fn:          b.run,
	}
}

func (b *Bridge) run(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	if b == nil || b.manager == nil {
		return core.Result{Err: fmt.Errorf("mcp: no servers configured")}, nil
	}
	defer b.manager.Acquire()()
	if b.manager.isClosed() {
		return core.Result{Err: fmt.Errorf("mcp: runtime closed; start a new conversation turn to reload configuration")}, nil
	}
	req, err := decodeBridgeRequest(raw)
	if err != nil {
		return core.Result{Err: fmt.Errorf("mcp_bridge: invalid arguments: %w", err)}, nil
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "list":
		return core.Result{Text: b.list()}, nil
	case "search":
		return core.Result{Text: core.HeadTail(b.search(ctx, req.Server, req.Query), bridgeResultHead, bridgeResultTail)}, nil
	case "call":
		if len(req.Calls) > 0 {
			return b.callBatch(ctx, req.Calls), nil
		}
		return b.call(ctx, req.Server, req.Tool, req.Arguments), nil
	default:
		return core.Result{Err: fmt.Errorf("mcp_bridge: action must be list, search, or call")}, nil
	}
}

// decodeBridgeRequest repairs a common local-model mix-up: the model copies
// the requested operation into the `tool` field (for example
// {"tool":"search","query":"browser"}) and omits `action`. The alias is
// accepted only when action is absent and tool is exactly list or search.
// `call` is deliberately not an alias because that same field carries the
// remote MCP tool name; mutating calls must keep the unambiguous canonical
// action=call form.
func decodeBridgeRequest(raw json.RawMessage) (bridgeRequest, error) {
	var req bridgeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return bridgeRequest{}, err
	}
	if strings.TrimSpace(req.Action) != "" {
		return req, nil
	}
	alias := strings.ToLower(strings.TrimSpace(req.Tool))
	switch alias {
	case "list", "search":
		req.Action = alias
		// list/search do not consume a remote tool name. Clearing the alias
		// prevents it from being mistaken for one by future code.
		req.Tool = ""
	}
	return req, nil
}

func (b *Bridge) list() string {
	rows := make([]map[string]any, 0, len(b.manager.Names()))
	for _, name := range b.manager.Names() {
		s, _ := b.manager.Get(name)
		status := s.Status()
		rows = append(rows, map[string]any{
			"server": name, "description": s.Config.Description, "tags": s.Config.Tags,
			"portable": s.Config.Portable, "running": status.Running, "tools": status.Tools,
			"error": status.Err,
		})
	}
	encoded, _ := json.Marshal(rows)
	return string(encoded)
}

func (b *Bridge) search(ctx context.Context, serverName, query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	serverName = strings.TrimSpace(serverName)
	candidates := make([]string, 0)
	if serverName != "" {
		if _, ok := b.manager.Get(serverName); !ok {
			return fmt.Sprintf("unknown MCP server %q; use action=list", serverName)
		}
		candidates = append(candidates, serverName)
	} else {
		for _, name := range b.manager.Names() {
			s, _ := b.manager.Get(name)
			haystack := strings.ToLower(name + " " + s.Config.Description + " " + strings.Join(s.Config.Tags, " "))
			if query == "" || strings.Contains(haystack, query) {
				candidates = append(candidates, name)
			}
		}
	}
	if len(candidates) == 0 {
		return "no MCP server metadata matched; use action=list, then search with an exact server"
	}
	type foundTool struct {
		Server      string          `json:"server"`
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"input_schema,omitempty"`
		ReadOnly    bool            `json:"read_only,omitempty"`
		Declared    bool            `json:"declared,omitempty"`
	}
	var found []foundTool
	var failures []string
	for _, name := range candidates {
		s, _ := b.manager.Get(name)
		// Manifest-declared tools allow discovery without a process; the
		// process starts only on call. Plain servers start lazily as before.
		declared := !s.running() && len(s.Config.DeclaredTools) > 0
		if !declared {
			if err := s.Start(ctx); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", name, err))
				continue
			}
		}
		for _, tool := range s.ListedTools() {
			if !s.ToolAllowed(tool.Name) {
				continue
			}
			haystack := strings.ToLower(tool.Name + " " + tool.Description)
			if query != "" && serverName != "" && !strings.Contains(haystack, query) {
				continue
			}
			found = append(found, foundTool{Server: name, Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
				ReadOnly: s.ToolReadOnly(tool.Name), Declared: declared})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Server != found[j].Server {
			return found[i].Server < found[j].Server
		}
		return found[i].Name < found[j].Name
	})
	payload := map[string]any{"tools": found}
	if len(failures) > 0 {
		payload["errors"] = failures
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func (b *Bridge) call(ctx context.Context, serverName, toolName string, arguments json.RawMessage) core.Result {
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	if serverName == "" || toolName == "" {
		return core.Result{Err: fmt.Errorf("mcp_bridge call requires server and tool")}
	}
	s, ok := b.manager.Get(serverName)
	if !ok {
		return core.Result{Err: fmt.Errorf("unknown MCP server %q", serverName)}
	}
	if err := s.Start(ctx); err != nil {
		return core.Result{Err: err}
	}
	known := false
	for _, tool := range s.Tools() {
		if tool.Name == toolName {
			known = true
			break
		}
	}
	if !known {
		return core.Result{Err: fmt.Errorf("MCP server %s has no tool %q; use action=search", serverName, toolName)}
	}
	arguments, err := normalizeBridgeArguments(arguments)
	if err != nil {
		return core.Result{Err: fmt.Errorf("mcp_bridge call arguments: %w", err)}
	}
	result, err := s.CallTool(ctx, toolName, arguments)
	if err != nil {
		return core.Result{Err: err}
	}
	if result.IsError {
		text := core.HeadTail(result.Text, bridgeResultHead, bridgeResultTail)
		return core.Result{Err: fmt.Errorf("mcp %s/%s: %s", serverName, toolName, text)}
	}
	return shapeResult(result, s.imageModeFor(toolName, b.manager.ImageMode()), bridgeResultHead, bridgeResultTail)
}

// callBatch runs up to maxBridgeBatch calls sequentially (simple and safe for
// stateful servers such as browsers). Each item gets its own text section;
// an item failure is reported in its section and never aborts the others.
// The whole batch fails only when every item failed.
func (b *Bridge) callBatch(ctx context.Context, calls []bridgeCall) core.Result {
	if len(calls) > maxBridgeBatch {
		return core.Result{Err: fmt.Errorf("mcp_bridge: at most %d calls per batch (got %d)", maxBridgeBatch, len(calls))}
	}
	var out core.Result
	var text, retained strings.Builder
	failures, truncated := 0, false
	for i, item := range calls {
		if err := ctx.Err(); err != nil {
			return core.Result{Err: err}
		}
		header := fmt.Sprintf("## [%d/%d] %s/%s\n", i+1, len(calls), strings.TrimSpace(item.Server), strings.TrimSpace(item.Tool))
		res := b.call(ctx, item.Server, item.Tool, item.Arguments)
		body := res.Text
		full := res.Text
		if res.RetainedText != "" {
			full = res.RetainedText
		}
		if res.Err != nil {
			failures++
			body = "error: " + res.Err.Error()
			full = body
		}
		text.WriteString(header + body + "\n\n")
		retained.WriteString(header + full + "\n\n")
		truncated = truncated || res.RetainedText != ""
		out.Images = append(out.Images, res.Images...)
		out.PreviewImages = append(out.PreviewImages, res.PreviewImages...)
		out.Files = append(out.Files, res.Files...)
		out.MediaNote = out.MediaNote || res.MediaNote
	}
	out.Text = strings.TrimRight(text.String(), "\n")
	if truncated {
		// Full sections stay readable through read_output; Text keeps previews.
		out.RetainedText = strings.TrimRight(retained.String(), "\n")
	}
	if failures == len(calls) {
		return core.Result{Err: fmt.Errorf("mcp_bridge: all %d batched calls failed:\n%s", len(calls), out.Text)}
	}
	return out
}

// normalizeBridgeArguments accepts the object required by MCP and repairs the
// common small-model form where that object is emitted as a JSON-encoded
// string. Without this repair the string reaches the MCP server unchanged, so
// required fields appear to be missing even though they are visible in the
// model's tool call.
func normalizeBridgeArguments(raw json.RawMessage) (json.RawMessage, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`), nil
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, fmt.Errorf("decode stringified JSON: %w", err)
		}
		raw = json.RawMessage(strings.TrimSpace(encoded))
	}
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, fmt.Errorf("must be a JSON object (a stringified object is also accepted)")
	}
	return raw, nil
}
