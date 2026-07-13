package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"supercli/internal/system/childproc"
)

// ServerConfig mirrors a [mcp.servers.<name>] section in config.toml.
type ServerConfig struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Env         map[string]string `toml:"env"`
	Dir         string            `toml:"-"`
	Description string            `toml:"-"`
	Portable    bool              `toml:"-"`
	PackageDir  string            `toml:"-"`
	PackageID   string            `toml:"-"`
	Tags        []string          `toml:"-"`
}

// Server is one configured MCP server: a subprocess speaking JSON-RPC
// over stdio plus the tool list discovered at startup.
type Server struct {
	Name   string
	Config ServerConfig

	mu      sync.Mutex
	cmd     *exec.Cmd
	client  *Client
	tools   []ToolDef
	lastErr string
	started time.Time
}

// Status is a display snapshot for /mcp.
type Status struct {
	Name        string
	Command     string
	Description string
	Portable    bool
	PackageID   string
	PackageDir  string
	Tags        []string
	Running     bool
	Tools       int
	Err         string
	Uptime      time.Duration
}

// Start spawns the subprocess, performs the initialize handshake, and
// fetches the tool list. Idempotent: a running server is left alone.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return nil
	}
	if s.Config.Command == "" {
		s.lastErr = "command is empty"
		return fmt.Errorf("mcp server %s: command is empty", s.Name)
	}
	cmd := exec.Command(s.Config.Command, s.Config.Args...)
	childproc.HideWindow(cmd)
	if s.Config.Dir != "" {
		cmd.Dir = s.Config.Dir
	}
	cmd.Env = os.Environ()
	for k, v := range s.Config.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("mcp server %s: stdin: %w", s.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("mcp server %s: stdout: %w", s.Name, err)
	}
	if err := cmd.Start(); err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("mcp server %s: start %q: %w", s.Name, s.Config.Command, err)
	}
	client := NewClient(stdout, stdin)
	if _, err := client.Initialize(ctx); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		s.lastErr = err.Error()
		return fmt.Errorf("mcp server %s: initialize: %w", s.Name, err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		s.lastErr = err.Error()
		return fmt.Errorf("mcp server %s: tools/list: %w", s.Name, err)
	}
	s.cmd = cmd
	s.client = client
	s.tools = tools
	s.lastErr = ""
	s.started = time.Now()
	return nil
}

// Stop kills the subprocess. Safe to call when not running.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return nil
	}
	err := s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	s.cmd = nil
	s.client = nil
	return err
}

// Tools returns the tool list discovered at Start.
func (s *Server) Tools() []ToolDef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tools
}

// CallTool proxies tools/call to the running subprocess.
func (s *Server) CallTool(ctx context.Context, name string, args []byte) (Result, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return Result{}, fmt.Errorf("mcp server %s is not running (try /mcp restart %s)", s.Name, s.Name)
	}
	return client.CallTool(ctx, name, args)
}

// Status returns a display snapshot.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Name:        s.Name,
		Command:     s.Config.Command,
		Description: s.Config.Description,
		Portable:    s.Config.Portable,
		PackageID:   s.Config.PackageID,
		PackageDir:  s.Config.PackageDir,
		Tags:        append([]string(nil), s.Config.Tags...),
		Running:     s.cmd != nil,
		Tools:       len(s.tools),
		Err:         s.lastErr,
	}
	if st.Running {
		st.Uptime = time.Since(s.started)
	}
	return st
}

// Manager owns all configured MCP servers.
type Manager struct {
	mu      sync.RWMutex
	servers map[string]*Server
}

func NewManager(configs map[string]ServerConfig) *Manager {
	m := &Manager{servers: make(map[string]*Server, len(configs))}
	for name, cfg := range configs {
		m.servers[name] = &Server{Name: name, Config: cfg}
	}
	return m
}

// Names returns the configured server names, sorted.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.servers))
	for n := range m.servers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Get returns a server by name.
func (m *Manager) Get(name string) (*Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.servers[name]
	return s, ok
}

// StartAll starts every configured server, collecting (not aborting on)
// per-server errors.
func (m *Manager) StartAll(ctx context.Context) map[string]error {
	errs := make(map[string]error)
	for _, name := range m.Names() {
		s, _ := m.Get(name)
		if err := s.Start(ctx); err != nil {
			errs[name] = err
		}
	}
	return errs
}

// Restart stops and re-starts one server.
func (m *Manager) Restart(ctx context.Context, name string) error {
	s, ok := m.Get(name)
	if !ok {
		return fmt.Errorf("unknown MCP server %q (configured: %v)", name, m.Names())
	}
	_ = s.Stop()
	return s.Start(ctx)
}

// StopAll stops every server (process shutdown).
func (m *Manager) StopAll() {
	for _, name := range m.Names() {
		if s, ok := m.Get(name); ok {
			_ = s.Stop()
		}
	}
}

// Statuses returns display snapshots for every server, sorted by name.
func (m *Manager) Statuses() []Status {
	out := make([]Status, 0)
	for _, name := range m.Names() {
		if s, ok := m.Get(name); ok {
			out = append(out, s.Status())
		}
	}
	return out
}
