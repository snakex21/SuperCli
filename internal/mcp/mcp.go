// Package mcp implements a minimal Model Context Protocol client
// for SuperCli. F2.f provides the interface shape, the JSON-RPC
// transport, and a mock server for tests; the production servers
// (semble, headroom, context7) land in F4.
//
// References:
//   - https://modelcontextprotocol.io/specification/2025-06-18
//   - https://github.com/modelcontextprotocol/specification
package mcp

import (
	"context"
	"fmt"
)

// Server is the contract an MCP server integration must
// satisfy. The Tools method returns tools.Tool-shaped wrappers
// that can be registered directly with tools.Registry.
type Server interface {
	// Name returns the server's identifier (e.g. "semble").
	// It is used as the prefix for tool names registered by
	// this server.
	Name() string
	// Start brings the server online. It should be idempotent.
	Start(ctx context.Context) error
	// Stop shuts the server down. Safe to call multiple times.
	Stop() error
	// Tools returns the tools this server exposes. It may
	// return an empty list if the server is not running.
	Tools() []ToolDef
	// CallTool invokes a tool by name with the given JSON
	// arguments. Errors returned here are surfaced as the
	// tool's execute error.
	CallTool(ctx context.Context, name string, args []byte) (Result, error)
}

// ToolDef is the static description of a tool exposed by an
// MCP server. It mirrors tools.Tool's "spec" half.
type ToolDef struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object describing the
	// accepted arguments. It is passed through verbatim into
	// the registered tools.Tool.Schema.
	InputSchema map[string]any
}

// Result is what an MCP server returns from CallTool. The
// conversion to tools.Result (text or image) is done by the
// wrapper that registers the tool.
type Result struct {
	Text    string
	Image   *Image
	IsError bool
}

// Image is a returned image from an MCP tool call.
type Image struct {
	MediaType string
	Data      []byte
}

// StdServer is the optional, additional contract an in-process
// server may implement to short-circuit JSON-RPC for testing
// or for libraries that run in the same process. The default
// transport expects an external subprocess.
type StdServer interface {
	Server
	// HandleRequest processes a single JSON-RPC request and
	// returns the response payload (the "result" field). The
	// client handles request id correlation and protocol
	// framing.
	HandleRequest(ctx context.Context, method string, params []byte) ([]byte, error)
}

// ProtocolError signals a JSON-RPC or MCP protocol violation.
type ProtocolError struct {
	Code    int
	Message string
	Data    []byte
}

// Error implements the error interface.
func (e *ProtocolError) Error() string {
	return fmt.Sprintf("mcp: protocol error %d: %s", e.Code, e.Message)
}
