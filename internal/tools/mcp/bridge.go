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

func NewBridge(manager *Manager) *Bridge { return &Bridge{manager: manager} }

func (b *Bridge) Spec() core.Tool {
	return core.Tool{
		Name:        "mcp_bridge",
		Description: "Access configured MCP capabilities, including web/browser tools. list is local; search starts a matching server lazily; call invokes one tool.",
		Schema:      `{"type":"object","properties":{"action":{"type":"string","enum":["list","search","call"]},"server":{"type":"string","description":"MCP server name"},"query":{"type":"string","description":"Tool/server capability to find"},"tool":{"type":"string","description":"Exact remote MCP tool name"},"arguments":{"type":"object","additionalProperties":true}},"required":["action"]}`,
		Fn:          b.run,
	}
}

func (b *Bridge) run(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	if b == nil || b.manager == nil {
		return core.Result{Err: fmt.Errorf("mcp: no servers configured")}, nil
	}
	var req struct {
		Action    string          `json:"action"`
		Server    string          `json:"server"`
		Query     string          `json:"query"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return core.Result{Err: fmt.Errorf("mcp_bridge: invalid arguments: %w", err)}, nil
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "list":
		return core.Result{Text: b.list()}, nil
	case "search":
		return core.Result{Text: core.HeadTail(b.search(ctx, req.Server, req.Query), bridgeResultHead, bridgeResultTail)}, nil
	case "call":
		return b.call(ctx, req.Server, req.Tool, req.Arguments), nil
	default:
		return core.Result{Err: fmt.Errorf("mcp_bridge: action must be list, search, or call")}, nil
	}
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
	}
	var found []foundTool
	var failures []string
	for _, name := range candidates {
		s, _ := b.manager.Get(name)
		if err := s.Start(ctx); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for _, tool := range s.Tools() {
			haystack := strings.ToLower(tool.Name + " " + tool.Description)
			if query != "" && serverName != "" && !strings.Contains(haystack, query) {
				continue
			}
			found = append(found, foundTool{Server: name, Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
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
	text := core.HeadTail(result.Text, bridgeResultHead, bridgeResultTail)
	if result.IsError {
		return core.Result{Err: fmt.Errorf("mcp %s/%s: %s", serverName, toolName, text)}
	}
	return core.Result{Text: text}
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
