// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"supercli/internal/account/pricing"
	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/mcp"
)

// openDataDB opens the shared SuperCli SQLite database in dataDir.
// Callers own the returned handle and must Close it. Kept here so the
// data-panel helpers share one consistent open path with the CLI.
func openDataDB(dataDir string) (*sql.DB, error) {
	return storage.OpenAt(dataDir)
}

// Engine holds the long-lived pieces a web session needs to talk to
// the model. It is built once at server start from the resolved
// config and data directory, mirroring the CLI's runBatch wiring.
type Engine struct {
	mu         sync.RWMutex
	cfg        config.Config
	dataDir    string
	home       string
	appProfile string
	caps       *llm.CapabilityRegistry
	prov       llm.Provider
	// factory is the single provider-construction funnel: every
	// provider (main, model switches, task workers) comes out of it
	// wrapped in llm.Metered, so purpose labels, the background gate
	// and foreground preemption apply to web calls exactly like CLI
	// calls. Per-session usage rows attach via llm.WithCallSink.
	factory *factory.Factory
	// titles defers the LLM session-title summary until after the
	// first answer finished and the session sat idle; the immediate
	// title is deterministic and local (see title.go).
	titles *titleScheduler
	// learned holds per-model context limits persisted from past
	// context-length errors (<dataDir>/context_limits.json), shared
	// with the CLI so both front-ends size auto-compaction the same.
	learned    *llm.LearnedLimits
	questionMu sync.Mutex
	questions  map[string]tools.AskRequest
	perfMu     sync.RWMutex
	perf       map[string]providerCallPerformance
	// offTurn counts model calls the GUI makes OUTSIDE any agent turn
	// (titles, run summaries, folder/document indexing, vision). They
	// belong to no turn, so no turn summary can carry them, but they
	// still cost the user time and money. Process-lifetime counters,
	// see run_offturn.go.
	offTurnMu    sync.Mutex
	offTurnCalls int
	offTurnUs    int64
	// sessions is opened lazily and shared by every web request. Store wraps
	// sql.DB and is safe for concurrent use; keeping one handle avoids running
	// SQLite Ping + the complete migration/FTS audit on every endpoint call.
	sessionMu sync.Mutex
	sessions  *session.Store
	closed    bool
	// errorLog is the shared append-only tool_errors.log handle. Web
	// runs build a fresh loop per request, so the file is opened once
	// per Engine and every loop appends to it; opening it per run
	// would mean a new file handle on every chat message.
	errorLogMu   sync.Mutex
	errorLog     *tools.ErrorLog
	errorLogOnce bool
	// goals is a shared service over the same portable SQLite database used by
	// the TUI. Keeping one handle per Engine avoids reopening and migrating the
	// database for every panel refresh and lets web agent tools observe UI edits
	// immediately.
	goalMu sync.Mutex
	goalDB *sql.DB
	goals  *goal.Service
	// Checkpoint metadata is workspace-specific. The GUI can hot-switch
	// projects, so cache one manager per canonical workspace rather than one
	// process-global manager.
	checkpointMu sync.Mutex
	checkpoints  map[string]*checkpoint.Manager
	// Language servers are workspace-scoped and expensive to initialize. Keep
	// one lazy code-intelligence tool per project and reuse it across requests.
	codeIntelMu sync.Mutex
	codeIntel   map[string]*tools.CodeIntel
	processMu   sync.Mutex
	processes   map[string]*tools.ProcessSession
	// Skill catalogs are workspace-scoped and immutable for the lifetime of a
	// process. Reuse each lazy Discoverer across UI browsing and agent turns so
	// external SKILL.md files are scanned once instead of once per request.
	skillMu      sync.Mutex
	skillCatalog map[string]*tools.Discoverer
	// mcpManager owns both explicit config.toml servers and relocatable
	// packages from <dataDir>/mcp. Servers remain stopped until mcp_bridge
	// searches or calls one of them.
	mcpMu      sync.RWMutex
	mcpManager *mcp.Manager
	// workers is process-wide for the web application. Short-lived HTTP loops
	// share it so active delegations remain observable and stoppable after a
	// panel refresh, while the registry's existing retention bounds memory.
	workers *agent.WorkerRegistry
	// diagnosticRegistry points at the most recently built complete tool
	// registry. Doctor only reads it; ordinary rendering never touches it.
	diagnosticMu       sync.RWMutex
	diagnosticRegistry *tools.Registry
	schedules          *scheduleManager
	modelContexts      *config.ModelContextStore
	// activeRuns counts foreground chat streams currently executing. The
	// native close handler combines it with active delegated workers so an
	// idle window closes immediately and only real work triggers a warning.
	activeRuns atomic.Int32
}

// NewEngine builds the provider and capability registry from the
// given config. home is the file-sandbox root; dataDir is where
// SuperCli state (db, memory, sessions) lives. A build failure is
// returned rather than panicking so the caller can surface it in the
// UI.
func NewEngine(cfg config.Config, home, dataDir string) (*Engine, error) {
	if home == "" {
		return nil, fmt.Errorf("webgui.NewEngine: home is empty")
	}
	caps, err := llm.NewCapabilityRegistryFromSources(dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("webgui.NewEngine: capabilities: %w", err)
	}
	if cachedPrices := pricing.LoadCache(dataDir); len(cachedPrices) > 0 {
		pricing.ApplyCachedRates(dataDir)
		applyWebPricingEntries(caps, cachedPrices)
	}
	f := factory.New(nil, dataDir, caps)
	tc, _ := config.ResolveConfig(dataDir, home, "")
	prov, err := f.BuildChain(cfg, tc, llm.PurposeMain)
	if err != nil {
		return nil, fmt.Errorf("webgui.NewEngine: provider: %w", err)
	}
	eng := &Engine{
		cfg:           cfg,
		dataDir:       dataDir,
		home:          home,
		caps:          caps,
		prov:          prov,
		factory:       f,
		learned:       llm.LoadLearnedLimits(dataDir),
		modelContexts: config.LoadModelContextStore(dataDir),
		questions:     make(map[string]tools.AskRequest),
		perf:          make(map[string]providerCallPerformance),
		checkpoints:   make(map[string]*checkpoint.Manager),
		codeIntel:     make(map[string]*tools.CodeIntel),
		processes:     make(map[string]*tools.ProcessSession),
		skillCatalog:  make(map[string]*tools.Discoverer),
		workers:       agent.NewWorkerRegistry(),
	}
	eng.titles = newTitleScheduler(titleIdleDelay, eng.runSessionTitleLLM)
	eng.schedules = newScheduleManager(dataDir, func(workspace, prompt string) error {
		if !sameSessionWorkspace(workspace, eng.Home()) {
			return errors.New("scheduled workspace is not active")
		}
		_, err := eng.enqueueTask(context.Background(), "", prompt)
		return err
	})
	eng.providerManager().SetModelPrices(caps)
	if err := eng.reloadMCP(); err != nil {
		// A broken optional package must not prevent the application from
		// opening. Discovery diagnostics remain visible in the MCP panel.
		eng.mcpManager = nil
	}
	return eng, nil
}

func (e *Engine) beginActiveRun() func() {
	if e == nil {
		return func() {}
	}
	e.activeRuns.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() { e.activeRuns.Add(-1) })
	}
}

// HasActiveWork reports whether closing the desktop window would interrupt a
// foreground response or a delegated worker. Persisted queued tasks are not
// included because they survive application shutdown.
func (e *Engine) HasActiveWork() bool {
	if e == nil {
		return false
	}
	if e.activeRuns.Load() > 0 {
		return true
	}
	return e.workers != nil && e.workers.Counts().Running > 0
}

func explicitMCPConfigs(tc config.TomlConfig) map[string]mcp.ServerConfig {
	configs := make(map[string]mcp.ServerConfig, len(tc.Mcp.Servers))
	for name, sc := range tc.Mcp.Servers {
		configs[name] = mcp.ServerConfig{Command: sc.Command, Args: sc.Args, Env: sc.Env}
	}
	return configs
}

// reloadMCP atomically replaces the lazy MCP runtime after a configuration
// edit. Old subprocesses are stopped only after new discovery succeeded.
func (e *Engine) reloadMCP() error {
	tc, err := config.ResolveConfig(e.dataDir, e.Home(), "")
	if err != nil {
		return err
	}
	configs, _, err := mcp.LoadWorkspace(e.dataDir, explicitMCPConfigs(tc))
	if err != nil {
		return err
	}
	var next *mcp.Manager
	if len(configs) > 0 {
		next = mcp.NewManager(configs)
	}
	e.mcpMu.Lock()
	previous := e.mcpManager
	e.mcpManager = next
	e.mcpMu.Unlock()
	if previous != nil {
		previous.StopAll()
	}
	return nil
}

func (e *Engine) mcpRuntime() *mcp.Manager {
	e.mcpMu.RLock()
	defer e.mcpMu.RUnlock()
	return e.mcpManager
}

// sessionStore returns the Engine-owned session database. Opening is lazy so
// health/model-only uses do not pay for SQLite, while the first session request
// performs migrations exactly once for the lifetime of the Engine.
func (e *Engine) sessionStore() (*session.Store, error) {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("webgui engine is closed")
	}
	if e.sessions == nil {
		store, err := session.OpenStore(e.dataDir)
		if err != nil {
			return nil, err
		}
		e.sessions = store
	}
	return e.sessions, nil
}

// toolErrorLog returns the shared tool_errors.log writer, opening it on
// first use. A failure to open is logged once and then treated as
// "logging off" — diagnostics must never keep a chat from running.
func (e *Engine) toolErrorLog() agent.ErrorLogger {
	e.errorLogMu.Lock()
	defer e.errorLogMu.Unlock()
	if e.errorLogOnce {
		if e.errorLog == nil {
			return nil
		}
		return e.errorLog
	}
	e.errorLogOnce = true
	logsDir := filepath.Join(e.dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		log.Printf("webgui: mkdir logs: %v", err)
		return nil
	}
	l, err := tools.NewErrorLog(filepath.Join(logsDir, "tool_errors.log"))
	if err != nil {
		log.Printf("webgui: open tool error log: %v", err)
		return nil
	}
	e.errorLog = l
	return l
}

// goalService returns the Engine-owned goal service, opening and migrating the
// shared portable database once. Callers that need to observe changes made by
// another SuperCli process should call Refresh on the returned service.
func (e *Engine) goalService(ctx context.Context) (*goal.Service, error) {
	e.goalMu.Lock()
	defer e.goalMu.Unlock()
	e.sessionMu.Lock()
	closed := e.closed
	e.sessionMu.Unlock()
	if closed {
		return nil, fmt.Errorf("webgui engine is closed")
	}
	if e.goals != nil {
		return e.goals, nil
	}
	db, err := openDataDB(e.dataDir)
	if err != nil {
		return nil, err
	}
	storage := goal.NewStorage(db)
	if err := storage.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	svc := goal.NewService(storage)
	if _, err := svc.Refresh(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	e.goalDB = db
	e.goals = svc
	return svc, nil
}

// checkpointManager returns a long-lived manager for home. Manager serializes
// its mutable Git metadata internally; Engine only guards cache creation.
func (e *Engine) checkpointManager(home string) (*checkpoint.Manager, error) {
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	key := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	e.checkpointMu.Lock()
	defer e.checkpointMu.Unlock()
	if manager := e.checkpoints[key]; manager != nil {
		return manager, nil
	}
	manager, err := checkpoint.Open(abs, e.dataDir)
	if err != nil {
		return nil, err
	}
	e.checkpoints[key] = manager
	return manager, nil
}

func (e *Engine) clearCheckpointManagers() error {
	e.checkpointMu.Lock()
	defer e.checkpointMu.Unlock()
	var result error
	for key, manager := range e.checkpoints {
		if err := manager.Clear(); err != nil {
			result = errors.Join(result, err)
		}
		delete(e.checkpoints, key)
	}
	return errors.Join(result, os.RemoveAll(filepath.Join(e.dataDir, "checkpoints")))
}

// Close releases Engine-owned resources after the HTTP server has drained.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	if e.titles != nil {
		e.titles.Close()
	}
	if e.schedules != nil {
		e.schedules.Close()
		e.schedules = nil
	}
	e.mcpMu.Lock()
	mcpManager := e.mcpManager
	e.mcpManager = nil
	e.mcpMu.Unlock()
	if mcpManager != nil {
		mcpManager.StopAll()
	}
	e.codeIntelMu.Lock()
	codeIntel := e.codeIntel
	e.codeIntel = make(map[string]*tools.CodeIntel)
	e.codeIntelMu.Unlock()
	for _, tool := range codeIntel {
		tool.Close()
	}
	e.processMu.Lock()
	processes := e.processes
	e.processes = make(map[string]*tools.ProcessSession)
	e.processMu.Unlock()
	for _, tool := range processes {
		tool.Close()
	}
	e.errorLogMu.Lock()
	errorLog := e.errorLog
	e.errorLog = nil
	e.errorLogOnce = true
	e.errorLogMu.Unlock()
	if errorLog != nil {
		_ = errorLog.Close()
	}
	e.goalMu.Lock()
	goalDB := e.goalDB
	e.goalDB = nil
	e.goals = nil
	e.goalMu.Unlock()
	var goalErr error
	if goalDB != nil {
		goalErr = goalDB.Close()
	}
	e.sessionMu.Lock()
	if e.closed {
		e.sessionMu.Unlock()
		return nil
	}
	e.closed = true
	store := e.sessions
	e.sessions = nil
	e.sessionMu.Unlock()
	if store != nil {
		if err := store.Close(); err != nil {
			return err
		}
	}
	return goalErr
}
