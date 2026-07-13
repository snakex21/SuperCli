package webgui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"supercli/internal/system/config"
	"supercli/internal/tools/mcp"
)

// mcpServerView is the JSON shape for one MCP server in the UI.
type mcpServerView struct {
	Name        string            `json:"name"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Running     bool              `json:"running"`
	Tools       int               `json:"tools"`
	RuntimeErr  string            `json:"runtime_error,omitempty"`
	Description string            `json:"description,omitempty"`
}

type mcpPackageView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Manifest    string   `json:"manifest"`
	Enabled     bool     `json:"enabled"`
	Available   bool     `json:"available"`
	Error       string   `json:"error,omitempty"`
	Missing     []string `json:"missing,omitempty"`
	Running     bool     `json:"running"`
	Tools       int      `json:"tools"`
}

// handleMcpServers returns all configured MCP servers.
func (s *Server) handleMcpServers(w http.ResponseWriter, r *http.Request) {
	tc, err := config.LoadToml(filepath.Join(s.eng.dataDir, "config.toml"))
	if err != nil {
		tc = config.TomlConfig{}
	}
	statuses := make(map[string]mcp.Status)
	if manager := s.eng.mcpRuntime(); manager != nil {
		for _, status := range manager.Statuses() {
			statuses[status.Name] = status
		}
	}
	out := make([]mcpServerView, 0, len(tc.Mcp.Servers))
	for name, sc := range tc.Mcp.Servers {
		env := sc.Env
		if env == nil {
			env = map[string]string{}
		}
		status := statuses[name]
		out = append(out, mcpServerView{
			Name:       name,
			Command:    sc.Command,
			Args:       sc.Args,
			Env:        env,
			Running:    status.Running,
			Tools:      status.Tools,
			RuntimeErr: status.Err,
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	packages, discoverErr := mcp.DiscoverPortable(s.eng.dataDir)
	packageViews := make([]mcpPackageView, 0, len(packages))
	for _, pkg := range packages {
		status := statuses[pkg.Name]
		packageViews = append(packageViews, mcpPackageView{
			ID: pkg.ID, Name: pkg.Name, Version: pkg.Manifest.Version,
			Description: pkg.Manifest.Description, Manifest: pkg.ManifestRel,
			Enabled: pkg.Enabled, Available: pkg.Available, Error: pkg.Error,
			Missing: pkg.Missing, Running: status.Running, Tools: status.Tools,
		})
	}
	response := map[string]any{
		"servers": out, "packages": packageViews,
		"portable_path": filepath.Join(mcp.PortableDirName),
		"lazy":          true,
	}
	if discoverErr != nil {
		response["error"] = discoverErr.Error()
	}
	writeJSON(w, response)
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
	// Same guard as the other config writers: never save a zero struct
	// over an existing-but-unreadable config.toml.
	tc, err := config.LoadToml(path)
	if err != nil {
		http.Error(w, "load config: "+err.Error(), http.StatusInternalServerError)
		return
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
	if err := s.eng.reloadMCP(); err != nil {
		http.Error(w, "reload MCP: "+err.Error(), http.StatusInternalServerError)
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
	if err := s.eng.reloadMCP(); err != nil {
		http.Error(w, "reload MCP: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "name": name})
}

// handleMcpFolder opens the portable package workspace after an explicit user
// click. Unlike the project-folder endpoint this intentionally targets the
// application data directory, never an arbitrary request path.
func (s *Server) handleMcpFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := filepath.Join(s.eng.dataDir, mcp.PortableDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := openSystemFolder(root); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": root})
}
