package webgui

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"supercli/internal/system/config"
)

// mcpServerView is the JSON shape for one MCP server in the UI.
type mcpServerView struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// handleMcpServers returns all configured MCP servers.
func (s *Server) handleMcpServers(w http.ResponseWriter, r *http.Request) {
	tc, err := config.LoadToml(filepath.Join(s.eng.dataDir, "config.toml"))
	if err != nil {
		writeJSON(w, map[string]any{"servers": []mcpServerView{}})
		return
	}
	out := make([]mcpServerView, 0, len(tc.Mcp.Servers))
	for name, sc := range tc.Mcp.Servers {
		env := sc.Env
		if env == nil {
			env = map[string]string{}
		}
		out = append(out, mcpServerView{
			Name:    name,
			Command: sc.Command,
			Args:    sc.Args,
			Env:     env,
		})
	}
	writeJSON(w, map[string]any{"servers": out})
}

// handleMcpAdd adds or updates an MCP server in config.toml.
func (s *Server) handleMcpAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req mcpServerView
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.eng.dataDir, "config.toml")
	tc, err := config.LoadToml(path)
	if err != nil {
		tc = config.TomlConfig{}
	}
	if tc.Mcp.Servers == nil {
		tc.Mcp.Servers = make(map[string]config.McpServerConf)
	}
	tc.Mcp.Servers[name] = config.McpServerConf{
		Command: strings.TrimSpace(req.Command),
		Args:    req.Args,
		Env:     req.Env,
	}
	if err := config.SaveToml(path, tc); err != nil {
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "name": name})
}

// handleMcpRemove removes an MCP server from config.toml.
func (s *Server) handleMcpRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.eng.dataDir, "config.toml")
	tc, err := config.LoadToml(path)
	if err != nil {
		http.Error(w, "config not found", http.StatusNotFound)
		return
	}
	if tc.Mcp.Servers != nil {
		delete(tc.Mcp.Servers, name)
	}
	if err := config.SaveToml(path, tc); err != nil {
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "name": name})
}
