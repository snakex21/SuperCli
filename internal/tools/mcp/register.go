package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"supercli/internal/tools"
	core "supercli/internal/tools/core"
)

// Result cap for external MCP servers. A server is out of our
// control and can return many MB in one call; the boundary keeps
// the first 16 KB + last 4 KB (~ the 20k-char web_fetch default)
// with an explicit omitted_bytes marker, UTF-8-safe cuts.
const (
	mcpResultHeadBytes = 16 << 10
	mcpResultTailBytes = 4 << 10
)

// RegisterTools registers every tool of every *running* server into reg
// under the name mcp_<server>_<tool>. The tools are deliberately NOT
// marked always-on: the model discovers them via tool_search, so MCP
// schemas never bloat the default context.
//
// Returns the number of tools registered.
func RegisterTools(m *Manager, reg *tools.Registry) int {
	count := 0
	for _, name := range m.Names() {
		s, _ := m.Get(name)
		for _, td := range s.Tools() {
			if err := reg.Register(wrapTool(s, td)); err == nil {
				count++
			}
		}
	}
	return count
}

// wrapTool converts one MCP ToolDef into a tools.Tool that proxies
// execution to the server.
func wrapTool(s *Server, td ToolDef) tools.Tool {
	schema := string(td.InputSchema)
	if schema == "" {
		schema = `{"type":"object"}`
	}
	desc := td.Description
	if desc == "" {
		desc = "MCP tool " + td.Name + " from server " + s.Name
	}
	remote := td.Name
	return tools.Tool{
		Name:        fmt.Sprintf("mcp_%s_%s", s.Name, remote),
		Description: fmt.Sprintf("[MCP:%s] %s", s.Name, desc),
		Schema:      schema,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			res, err := s.CallTool(ctx, remote, args)
			if err != nil {
				return tools.Result{Err: err}, nil
			}
			if res.IsError {
				return tools.Result{Err: fmt.Errorf("mcp %s/%s: %s", s.Name, remote,
					core.HeadTail(res.Text, core.ModelContentTailBytes, core.ModelContentTailBytes))}, nil
			}
			return tools.Result{Text: core.HeadTail(res.Text, mcpResultHeadBytes, mcpResultTailBytes)}, nil
		},
	}
}
