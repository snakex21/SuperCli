package mcp

import (
	"context"
	"sync"
)

// MockServer is an in-process MCP server used for tests. It
// can be initialised, queried for tools, and asked to call any
// registered tool. Implementations register handlers in
// SetHandler.
type MockServer struct {
	name string

	mu       sync.Mutex
	tools    []ToolDef
	handlers map[string]func(ctx context.Context, args []byte) (Result, error)

	started bool
	stopped bool
}

// NewMockServer returns a server with the given name and no
// tools.
func NewMockServer(name string) *MockServer {
	return &MockServer{
		name:     name,
		handlers: make(map[string]func(ctx context.Context, args []byte) (Result, error)),
	}
}

// SetTool registers a tool definition and its handler. The
// handler is called when CallTool matches the tool's name.
func (m *MockServer) SetTool(def ToolDef, handler func(ctx context.Context, args []byte) (Result, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = append(m.tools, def)
	m.handlers[def.Name] = handler
}

// Name implements Server.
func (m *MockServer) Name() string { return m.name }

// Start implements Server.
func (m *MockServer) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

// Stop implements Server.
func (m *MockServer) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return nil
}

// Tools implements Server.
func (m *MockServer) Tools() []ToolDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ToolDef, len(m.tools))
	copy(out, m.tools)
	return out
}

// CallTool implements Server.
func (m *MockServer) CallTool(ctx context.Context, name string, args []byte) (Result, error) {
	m.mu.Lock()
	handler, ok := m.handlers[name]
	m.mu.Unlock()
	if !ok {
		return Result{IsError: true}, &ProtocolError{Code: -32601, Message: "tool not found: " + name}
	}
	return handler(ctx, args)
}

// Started reports whether Start was called.
func (m *MockServer) Started() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// Stopped reports whether Stop was called.
func (m *MockServer) Stopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}
