package webgui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var assetsFS embed.FS

// Handler builds the HTTP routing for the web GUI. It serves the
// embedded front-end at / and the JSON/SSE API under /api/. The
// returned handler is safe for concurrent use; each request opens its
// own short-lived stores so there is no shared mutable state.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static front-end from the embedded assets directory.
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// Embedded FS is built at compile time; a Sub failure is a
		// programming error, so panicking here is correct — it can
		// only fire in a broken build, never at runtime in the field.
		panic("webgui: embed assets: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// JSON read APIs for the side panels.
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/transcript", s.handleTranscript)
	mux.HandleFunc("/api/memory", s.handleMemory)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/goal", s.handleGoal)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/model", s.handleModel)
	mux.HandleFunc("/api/model/toggle", s.handleModelToggle)
	mux.HandleFunc("/api/model/default", s.handleModelDefault)
	mux.HandleFunc("/api/reasoning", s.handleReasoning)
	mux.HandleFunc("/api/orchestrator", s.handleOrchestrator)
	mux.HandleFunc("/api/config", s.handleConfigKnobs)
	mux.HandleFunc("/api/providers", s.handleProviders)
	mux.HandleFunc("/api/provider/scan", s.handleProviderScan)
	mux.HandleFunc("/api/codex/accounts", s.handleCodexAccounts)
	mux.HandleFunc("/api/codex/login", s.handleCodexLogin)
	mux.HandleFunc("/api/codex/logout", s.handleCodexLogout)
	mux.HandleFunc("/api/codex/refresh", s.handleCodexRefresh)
	mux.HandleFunc("/api/context", s.handleContext)

	// UI preferences (theme/fonts/keybinds/etc.) persisted server-side
	// so they survive restarts despite the per-launch random port that
	// would otherwise reset browser localStorage.
	mux.HandleFunc("/api/settings", s.handleUISettings)

	// MCP server management
	mux.HandleFunc("/api/mcp/servers", s.handleMcpServers)
	mux.HandleFunc("/api/mcp/add", s.handleMcpAdd)
	mux.HandleFunc("/api/mcp/remove", s.handleMcpRemove)

	// File browser & editor
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/file/read", s.handleFileRead)
	mux.HandleFunc("/api/file/write", s.handleFileWrite)

	// SSE chat stream.
	mux.HandleFunc("/api/chat", s.handleChat)

	// Native folder picker (Windows dialog)
	mux.HandleFunc("/api/folder-picker", s.handleFolderPicker)

	return s.withLocalGuard(mux)
}

// withLocalGuard rejects requests whose Host is not a loopback
// address. The GUI is a single-user local app launched in an
// app-mode window; binding to localhost plus this check keeps it off
// the network even if the OS resolves the listener more broadly.
func (s *Server) withLocalGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowRemote && !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden: local-only server", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
