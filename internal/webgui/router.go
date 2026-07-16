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
	if s.uiHandler != nil {
		mux.Handle("/", s.uiHandler)
	} else {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	}

	// JSON read APIs for the side panels.
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/transcript", s.handleTranscript)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/workers", s.handleWorkers)
	mux.HandleFunc("/api/doctor", s.handleDoctor)
	mux.HandleFunc("/api/branches", s.handleBranches)
	mux.HandleFunc("/api/memory", s.handleMemory)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/goal", s.handleGoal)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/model", s.handleModel)
	mux.HandleFunc("/api/model/price", s.handleModelPrice)
	mux.HandleFunc("/api/model/toggle", s.handleModelToggle)
	mux.HandleFunc("/api/model/visibility", s.handleModelVisibility)
	mux.HandleFunc("/api/model/default", s.handleModelDefault)
	mux.HandleFunc("/api/reasoning", s.handleReasoning)
	mux.HandleFunc("/api/orchestrator", s.handleOrchestrator)
	mux.HandleFunc("/api/config", s.handleConfigKnobs)
	mux.HandleFunc("/api/providers", s.handleProviders)
	mux.Handle("/api/provider/key/reveal", s.withSecretLocalOnly(http.HandlerFunc(s.handleProviderKeyReveal)))
	mux.HandleFunc("/api/provider/scan", s.handleProviderScan)
	mux.HandleFunc("/api/provider/diagnostics", s.handleProviderDiagnostics)
	mux.HandleFunc("/api/codex/accounts", s.handleCodexAccounts)
	mux.HandleFunc("/api/codex/login", s.handleCodexLogin)
	mux.HandleFunc("/api/codex/logout", s.handleCodexLogout)
	mux.HandleFunc("/api/codex/refresh", s.handleCodexRefresh)
	mux.HandleFunc("/api/context", s.handleContext)
	mux.HandleFunc("/api/context/compact", s.handleContextCompact)

	// UI preferences (theme/fonts/keybinds/etc.) persisted server-side
	// so they survive restarts despite the per-launch random port that
	// would otherwise reset browser localStorage.
	mux.HandleFunc("/api/settings", s.handleUISettings)
	mux.HandleFunc("/api/data/status", s.handleDataStatus)
	mux.HandleFunc("/api/data/clear", s.handleDataClear)
	mux.HandleFunc("/api/data/export", s.handleDataExport)
	mux.Handle("/api/data/export/full", s.withSecretLocalOnly(http.HandlerFunc(s.handleDataExportFull)))
	mux.Handle("/api/data/import", s.withSecretLocalOnly(http.HandlerFunc(s.handleDataImport)))

	// MCP server management
	mux.HandleFunc("/api/mcp/servers", s.handleMcpServers)
	mux.HandleFunc("/api/mcp/add", s.handleMcpAdd)
	mux.HandleFunc("/api/mcp/remove", s.handleMcpRemove)
	mux.HandleFunc("/api/mcp/folder", s.handleMcpFolder)

	// File browser & editor
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/file/read", s.handleFileRead)
	mux.HandleFunc("/api/file/write", s.handleFileWrite)

	// SSE chat stream.
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/question/answer", s.handleQuestionAnswer)
	mux.HandleFunc("/api/question/image", s.handleQuestionImage)
	mux.HandleFunc("/api/checkpoint", s.handleCheckpoint)
	mux.HandleFunc("/api/checkpoint/rewind", s.handleCheckpointRewind)
	mux.HandleFunc("/api/checkpoint/lesson", s.handleCheckpointLesson)
	mux.HandleFunc("/api/test/hard", s.handleHardTest)
	mux.HandleFunc("/api/prompt/profile", s.handlePromptProfile)
	mux.HandleFunc("/api/scratchpad", s.handleScratchpad)

	// Native folder picker (Windows dialog)
	mux.HandleFunc("/api/folder-picker", s.handleFolderPicker)
	mux.HandleFunc("/api/file-picker", s.handleFilePicker)
	mux.HandleFunc("/api/folder/open", s.handleOpenFolder)

	return s.withLocalGuard(mux)
}

// withSecretLocalOnly protects endpoints that return plaintext credentials.
// It deliberately ignores allowRemote so this endpoint never returns a stored
// key to a non-loopback peer. Host and the actual socket peer must both be
// loopback to also resist a spoofed Host header / DNS rebinding. This is an
// endpoint-specific safeguard; --allow-remote still exposes the rest of the
// unauthenticated GUI and must be used with care.
func (s *Server) withSecretLocalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) || !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden: secret is local-only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
