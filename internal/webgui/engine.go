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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"supercli/internal/account/pricing"
	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/llm/providers"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/system/preflight"
	systats "supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
	"supercli/internal/tools/mcp"
	"supercli/internal/tools/sandbox"
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
	mu      sync.RWMutex
	cfg     config.Config
	dataDir string
	home    string
	caps    *llm.CapabilityRegistry
	prov    llm.Provider
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
	// sessions is opened lazily and shared by every web request. Store wraps
	// sql.DB and is safe for concurrent use; keeping one handle avoids running
	// SQLite Ping + the complete migration/FTS audit on every endpoint call.
	sessionMu sync.Mutex
	sessions  *session.Store
	closed    bool
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
		cfg:         cfg,
		dataDir:     dataDir,
		home:        home,
		caps:        caps,
		prov:        prov,
		factory:     f,
		learned:     llm.LoadLearnedLimits(dataDir),
		questions:   make(map[string]tools.AskRequest),
		perf:        make(map[string]providerCallPerformance),
		checkpoints: make(map[string]*checkpoint.Manager),
		codeIntel:   make(map[string]*tools.CodeIntel),
		processes:   make(map[string]*tools.ProcessSession),
		workers:     agent.NewWorkerRegistry(),
	}
	eng.titles = newTitleScheduler(titleIdleDelay, eng.runSessionTitleLLM)
	eng.providerManager().SetModelPrices(caps)
	if err := eng.reloadMCP(); err != nil {
		// A broken optional package must not prevent the application from
		// opening. Discovery diagnostics remain visible in the MCP panel.
		eng.mcpManager = nil
	}
	return eng, nil
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

func (e *Engine) codeIntelFor(home string) *tools.CodeIntel {
	abs, key := workspaceCacheKey(home)
	e.codeIntelMu.Lock()
	defer e.codeIntelMu.Unlock()
	if tool := e.codeIntel[key]; tool != nil {
		return tool
	}
	tool := tools.NewCodeIntel(abs)
	e.codeIntel[key] = tool
	return tool
}

func (e *Engine) processSessionFor(home string) *tools.ProcessSession {
	abs, key := workspaceCacheKey(home)
	e.processMu.Lock()
	defer e.processMu.Unlock()
	if tool := e.processes[key]; tool != nil {
		return tool
	}
	tool := tools.NewProcessSession(abs)
	e.processes[key] = tool
	return tool
}

func workspaceCacheKey(home string) (string, string) {
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = filepath.Clean(home)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	key := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return abs, key
}

func (e *Engine) recordProviderPerformance(provider string, stat llm.CallStat) {
	if e == nil || strings.TrimSpace(provider) == "" {
		return
	}
	perf := providerCallPerformance{
		Model: stat.Model, TTFTMS: stat.TTFT.Milliseconds(), DurationMS: stat.Duration.Milliseconds(),
		TokensIn: stat.TokensIn, TokensOut: stat.TokensOut,
		Failed: stat.Failed, Canceled: stat.Canceled, CompletedAt: time.Now().UTC(),
	}
	if generated := stat.Duration - stat.TTFT; stat.TokensOut > 0 && generated > 0 {
		perf.TokensPerS = float64(stat.TokensOut) / generated.Seconds()
	}
	e.perfMu.Lock()
	e.perf[provider] = perf
	e.perfMu.Unlock()
}

func (e *Engine) providerPerformance(provider string) (providerCallPerformance, bool) {
	if e == nil {
		return providerCallPerformance{}, false
	}
	e.perfMu.RLock()
	defer e.perfMu.RUnlock()
	perf, ok := e.perf[provider]
	return perf, ok
}

// RefreshPricingAsync mirrors the CLI startup policy: a fresh cache is used
// immediately and network refresh, when needed, never delays the GUI opening.
func (e *Engine) RefreshPricingAsync() {
	if e == nil || e.caps == nil {
		return
	}
	if cached := pricing.LoadCache(e.dataDir); len(cached) > 0 && pricing.HasContextMetadata(cached) {
		return
	}
	fetcher := pricing.NewFetcher(e.dataDir)
	snapshot := e.caps.All()
	go func() {
		updated := fetcher.FetchAndUpdate(snapshot)
		applyWebModelInfo(e.caps, updated)
	}()
}

func applyWebPricingEntries(caps *llm.CapabilityRegistry, entries []pricing.PriceEntry) {
	infos := make([]llm.ModelInfo, 0, len(entries))
	for _, entry := range entries {
		infos = append(infos, llm.ModelInfo{
			ID: entry.ModelID, InputCost: entry.InputPer1M, OutputCost: entry.OutputPer1M,
			ContextLength: entry.ContextLength, Source: llm.SourceExternal, LastVerified: entry.FetchedAt,
		})
	}
	applyWebModelInfo(caps, infos)
}

func applyWebModelInfo(caps *llm.CapabilityRegistry, infos []llm.ModelInfo) {
	if caps == nil {
		return
	}
	for _, info := range infos {
		if info.ID == "" {
			continue
		}
		if existing, ok := caps.Get(info.ID); ok {
			if info.InputCost > 0 {
				existing.InputCost = info.InputCost
			}
			if info.OutputCost > 0 {
				existing.OutputCost = info.OutputCost
			}
			if existing.ContextLength == 0 && info.ContextLength > 0 {
				existing.ContextLength = info.ContextLength
			}
			if info.LastVerified.After(existing.LastVerified) {
				existing.LastVerified = info.LastVerified
			}
			caps.Register(existing)
			continue
		}
		caps.Register(info)
	}
}

// ModelName returns the active model id for display.
func (e *Engine) ModelName() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e == nil || e.prov == nil {
		return ""
	}
	return e.prov.Name()
}

// RuntimeSelection returns the browser-facing provider/model/reasoning tuple
// that future turns will use. Provider is the configured provider name when it
// can be resolved, falling back to the transport type for local/legacy setups.
func (e *Engine) RuntimeSelection() (provider, model, reasoning string) {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	model = cfg.Model
	for _, configured := range e.providerManager().Configured() {
		if configured.Type == cfg.Provider && strings.TrimRight(configured.BaseURL, "/") == strings.TrimRight(cfg.BaseURL, "/") {
			provider = configured.Name
			if configured.Model == model {
				break
			}
		}
	}
	if provider == "" {
		provider = e.caps.Provider(model)
	}
	if provider == "" {
		provider = cfg.Provider
	}
	return provider, model, llm.ReasoningEffort()
}

// ReasoningSupportKey returns the backend-scoped key used by the OpenAI-
// compatible provider's live effort negotiation. Native providers retain the
// model-only key used by their request builders.
func (e *Engine) ReasoningSupportKey() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Provider == config.ProviderOpenAI || e.cfg.Provider == config.ProviderResponses || e.cfg.Provider == config.ProviderOpencode {
		model := e.cfg.Model
		if e.cfg.Provider == config.ProviderOpencode {
			model = strings.TrimPrefix(model, "opencode/")
		}
		return llm.ReasoningSupportKey(e.cfg.BaseURL, model)
	}
	return e.cfg.Model
}

// SupportsUnifiedReasoningGateway reports whether the active OpenAI-compatible
// endpoint performs its own reasoning-effort mapping (Kilo/OpenRouter/OpenCode).
func (e *Engine) SupportsUnifiedReasoningGateway() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cfg.Provider != config.ProviderOpenAI && e.cfg.Provider != config.ProviderOpencode {
		return false
	}
	return llm.IsUnifiedReasoningGateway(e.cfg.BaseURL)
}

// Home returns the file-sandbox root.
func (e *Engine) Home() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.home
}

// setHome updates the file-sandbox root for future web requests. Web runs
// build a fresh agent loop per /api/chat request, so unlike the TUI this can
// take effect immediately for subsequent chats and file-browser calls.
func (e *Engine) setHome(home string) {
	e.mu.Lock()
	e.home = home
	e.mu.Unlock()
	// Project config may add or override MCP servers. Refresh metadata now;
	// no process is started until a later bridge call.
	_ = e.reloadMCP()
}

// DataDir returns the SuperCli data directory.
func (e *Engine) DataDir() string { return e.dataDir }

// newLoop builds a fresh agent loop with the standard always-on file
// tool set. Each web run gets its own loop so concurrent browser tabs
// do not share mutable conversation state. Mirrors runBatch's
// registry so the web agent can actually read and edit files.
func (e *Engine) newLoop() (*agent.Loop, error) {
	return e.newLoopWithSession(nil, nil)
}

// newLoopWithSession builds a loop seeded with prior conversation messages and
// an optional session writer. The web GUI uses this to keep one browser chat as
// one persisted SuperCli session across multiple /api/chat requests.
func (e *Engine) newLoopWithSession(initial []llm.Message, writer agent.SessionWriter) (*agent.Loop, error) {
	return e.newLoopWithSessionAt(initial, writer, e.Home())
}

// newLoopWithSessionAt pins one run to the workspace captured before session
// creation/validation. A concurrent project switch affects future requests but
// cannot move an in-flight run's tools into another sandbox.
func (e *Engine) newLoopWithSessionAt(initial []llm.Message, writer agent.SessionWriter, home string) (*agent.Loop, error) {
	return e.newLoopWithSessionAtUsage(initial, writer, home)
}

func (e *Engine) newLoopWithSessionAtUsage(initial []llm.Message, writer agent.SessionWriter, home string) (*agent.Loop, error) {
	return e.newLoopWithSessionAtUsageAsk(initial, writer, home, nil)
}

func (e *Engine) newLoopWithSessionAtUsageAsk(initial []llm.Message, writer agent.SessionWriter, home string, askCh chan<- tools.AskRequest) (*agent.Loop, error) {
	return e.newLoopWithSessionAtUsageInteractive(initial, writer, home, askCh, nil)
}

func (e *Engine) newLoopWithSessionAtUsageInteractive(initial []llm.Message, writer agent.SessionWriter, home string, askCh chan<- tools.AskRequest, turn *checkpoint.Turn, telemetry ...systats.Recorder) (*agent.Loop, error) {
	e.mu.RLock()
	prov := e.prov
	caps := e.caps
	cfg := e.cfg
	e.mu.RUnlock()
	tc := e.tomlConfigAt(home)
	catalogHoist := strings.EqualFold(strings.TrimSpace(os.Getenv("SUPERCLI_CATALOG_HOIST")), "true") || strings.TrimSpace(os.Getenv("SUPERCLI_CATALOG_HOIST")) == "1"
	execProfile := execution.Resolve(cfg, tc, caps, catalogHoist)
	var recorder systats.Recorder
	if len(telemetry) > 0 {
		recorder = telemetry[0]
	}
	// Tri-state contract:
	//   nil   = adaptive delegation (full parent tools + optional task workers)
	//   true  = hard orchestrator (parent restricted; substantial work delegates)
	//   false = direct mode (worker tools physically absent)
	orchestrator := tc.Orchestrator != nil && *tc.Orchestrator
	delegation := tc.Orchestrator == nil || *tc.Orchestrator
	taskParallel, taskParallelWarnLocal := execution.Parallel(cfg.BaseURL, tc.TaskParallel)
	goalSvc, err := e.goalService(context.Background())
	if err != nil {
		return nil, fmt.Errorf("goal service: %w", err)
	}
	if _, err := goalSvc.Refresh(context.Background()); err != nil {
		return nil, fmt.Errorf("refresh goal: %w", err)
	}
	reg := tools.NewRegistry()
	// Registered but not always-on: thin discovery keeps the LSP schema out of
	// ordinary chat turns, while the Engine reuses the lazy server per project.
	codeIntel := e.codeIntelFor(home)
	reg.MustRegister(codeIntel.Spec())
	reg.MustRegister(e.processSessionFor(home).Spec())
	discoverer := tools.NewDiscovererWithBuiltins(home, e.dataDir)
	skillApplier := tools.NewSkillApplier(discoverer)
	reg.MustRegister(skillApplier.Spec())
	reg.MarkAlwaysOn("apply_skill")
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
		tools.NewReadMany(home).Spec(),
		tools.NewReadImage(home, 0).Spec(),
		tools.NewListDir(home).Spec(),
		tools.NewEditLine(home).Spec(),
		tools.NewEditLines(home).Spec(),
		tools.NewInsertAfter(home).Spec(),
		tools.NewDeleteLines(home).Spec(),
		tools.NewWriteFile(home).Spec(),
		tools.NewMakeDir(home).Spec(),
		tools.NewMove(home).Spec(),
		tools.NewCopy(home).Spec(),
		tools.NewTrash(home).Spec(),
		tools.NewSearchCode(home).Spec(),
		tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec(),
		tools.NewScratchpad(home).Spec(),
	} {
		if turn != nil {
			sp = turn.Wrap(sp)
		}
		sp = codeIntel.WrapMutation(sp)
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
	invoke := agent.NewInvokeTool(reg).Spec()
	if turn != nil {
		invoke = turn.Wrap(invoke)
	}
	reg.MustRegister(invoke)
	reg.MarkAlwaysOn("invoke_tool")
	// Keep the full goal schema out of ordinary turns. Once a goal is active it
	// is no longer speculative: expose the schema from the first request so a
	// slow/local model does not spend a fresh inference rediscovering it before
	// every state transition. Stable toolsets pay this prefix once in KV cache.
	goalSpec := tools.NewGoalTool(goalSvc).Spec()
	reg.MustRegister(goalSpec)
	if goalSvc != nil && goalSvc.Active() != nil {
		reg.MarkAlwaysOn(goalSpec.Name)
	}
	if askCh != nil {
		ask := tools.NewAskUser(askCh)
		// Visual decisions often take longer than a text choice, especially
		// when the user opens previews. The run context still cancels promptly.
		ask.Timeout = 10 * time.Minute
		reg.MustRegister(ask.Spec())
		reg.MarkAlwaysOn("ask_user")
	}
	// Current facts should not fall through to browser automation. Keep one
	// tiny query-only tool always available, while the larger filtered search
	// and fetch schemas remain discoverable through tool_search.
	reg.MustRegister(tools.NewWebFetch().Spec())
	webEngine := tc.WebSearch.Engine
	webKey := tc.WebSearch.APIKey
	if webKey == "" {
		switch strings.ToLower(webEngine) {
		case "brave":
			webKey = os.Getenv("BRAVE_API_KEY")
		case "tavily":
			webKey = os.Getenv("TAVILY_API_KEY")
		}
	}
	webSearcher := tools.NewWebSearch(webEngine, webKey, tc.WebSearch.BaseURL)
	reg.MustRegister(webSearcher.Spec())
	reg.MustRegister(webSearcher.LookupSpec())
	reg.MarkAlwaysOn("web_lookup")
	if manager := e.mcpRuntime(); manager != nil && len(manager.Names()) > 0 {
		reg.MustRegister(mcp.NewBridge(manager).Spec())
		reg.MarkAlwaysOn("mcp_bridge")
	}
	// Web loops are short-lived and have a small registry. Lexical discovery
	// avoids opening an FTS database per request while preserving the exact
	// tool_search contract used by TUI and batch.
	toolSearcher := tools.NewToolSearcher(reg, nil)
	reg.MustRegister(toolSearcher.Spec())
	reg.MarkAlwaysOn("tool_search")
	// Context defense (mirrors the TUI wiring in app/main.go): without
	// WindowFor the loop assumes its 16384-token default for every
	// model, and without Summarizer auto-compaction degrades to the
	// blind hide fallback — the model silently loses the whole prior
	// conversation ("[earlier context cleared]" with no summary).
	windowFor := func(model string) int {
		if tc.ContextWindow > 0 {
			return tc.ContextWindow
		}
		if info, ok := caps.Get(model); ok && info.ContextLength > 0 {
			return info.ContextLength
		}
		if v := e.learned.Get(model); v > 0 {
			return v
		}
		return 0 // loop falls back to its 16384 default
	}
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:              prov,
		Registry:              reg,
		Caps:                  caps,
		System:                webAgentSystemPrompt(home, e.dataDir, cfg.Model, execProfile.PromptSmall, orchestrator, delegation, goalSvc),
		MaxSteps:              25,
		Orchestrator:          orchestrator,
		TaskParallel:          taskParallel,
		TaskParallelWarnLocal: taskParallelWarnLocal,
		EnableNavigator:       execProfile.EnableNavigator,
		NavigatorAuto:         execProfile.NavigatorAuto,
		NavigatorKeywordsOnly: execProfile.NavigatorKeywordsOnly,
		ThinTools:             execProfile.ThinTools,
		StableToolset:         execProfile.StableToolset,
		CatalogHoist:          execProfile.CatalogHoist,
		BaseDir:               home,
		InitialMessages:       initial,
		Writer:                writer,
		WindowFor:             windowFor,
		Summarizer:            agent.NewAutoSummarizer(reg.ActiveNames),
		LearnLimit:            e.learned.Learn,
		// Zero-LLM tool-result prune (first line of context defense,
		// before the summary fallback). Config prune_protect_tokens:
		// 0 = default 8192-token protected tail, negative = off.
		PruneProtectTokens: tc.PruneProtectTokens,
		Stats:              recorder,
	})
	if err != nil {
		return nil, err
	}
	if delegation {
		// AUTO and ON expose workers. Explicit OFF skips this block, so task,
		// send_message and task_stop cannot be discovered or executed.
		if err := e.wireTaskTool(loop, reg, prov, caps, home, tc); err != nil {
			return nil, err
		}
	}
	e.diagnosticMu.Lock()
	e.diagnosticRegistry = reg
	e.diagnosticMu.Unlock()
	if orchestrator {
		// Workers retain the complete base registry above; only the parent loop
		// is physically restricted to delegation and read-only lookup tools.
		loop.SetRegistry(agent.OrchestratorRegistry(reg))
	}
	return loop, nil
}

func webAgentSystemPrompt(home, dataDir, model string, promptSmall, orchestrator, delegation bool, goalSvc *goal.Service) string {
	system := llmprompt.Build(promptSmall)
	if profile := llmprompt.LoadProfileAt(home, dataDir, model); profile != "" {
		system += "\n\n" + profile
	}
	if orchestrator {
		system += agent.OrchestratorPrompt()
	} else if delegation {
		system += agent.CoordinatorPrompt()
	}
	// An HTTP/SSE run ends with the parent turn. Background task notifications
	// cannot be delivered to a closed response, so web coordinators deliberately
	// use synchronous task calls.
	if delegation {
		system += "\n\nWeb GUI: call task synchronously; do not request async/background workers."
	}
	if sandbox.IsUnsandboxed() {
		system += fmt.Sprintf("\n\nActive workspace: %s\nFull filesystem access is ON. File and search tools may use absolute paths outside this workspace; sensitive system folders remain blocked.", home)
	} else {
		system += fmt.Sprintf("\n\nActive workspace (all file and shell tools are sandboxed here): %s", home)
	}
	// A tiny discovery hint is cheaper than carrying the complete goal schema in
	// every request. When a goal is active, inject its open steps directly.
	system += "\n\nFor durable multi-step work, find and use the goal tool."
	if goalSvc != nil {
		if injected, err := goalSvc.Inject(context.Background(), system, 5); err == nil {
			system = injected
		}
	}
	return system
}

// wireTaskTool registers the task / send_message / task_stop tools on
// reg, honouring the config knobs the CLI honours: task_max_steps,
// task_max_tokens, task_model (worker backend override) and
// preflight_repo (cold-context repo briefing for workers).
func (e *Engine) wireTaskTool(loop *agent.Loop, reg *tools.Registry, prov llm.Provider, caps *llm.CapabilityRegistry, home string, tc config.TomlConfig) error {
	subReg := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(subReg, agent.BuiltinSubAgents())
	at, err := agent.NewAgentTool(subReg, loop, reg, prov, caps,
		func(cfg agent.LoopConfig) (*agent.Loop, error) { return agent.NewLoop(cfg) })
	if err != nil {
		return fmt.Errorf("wire task delegation: %w", err)
	}
	at.Workers = e.workers
	at.MaxSteps = tc.TaskMaxSteps
	at.MaxTokens = tc.TaskMaxTokens
	if wp, _ := e.taskWorkerProvider(tc); wp != nil {
		// wp is metered by the factory (purpose "task"); the
		// per-session usage sink rides the run context, so no extra
		// wrapper is stacked here.
		at.WorkerProvider = wp
	}
	if tc.PreflightRepo == nil || *tc.PreflightRepo {
		at.Preflight = func() string { return preflight.Build(home, preflight.Options{}) }
	}
	for _, sp := range []tools.Tool{at.Spec(), agent.NewSendMessageTool(at.Workers).Spec(), agent.NewTaskStopTool(at.Workers).Spec()} {
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
	return nil
}

// tomlConfig returns the merged (global + project) config.toml view,
// degrading to the zero value when nothing is on disk.
func (e *Engine) tomlConfig() config.TomlConfig {
	return e.tomlConfigAt(e.Home())
}

func (e *Engine) tomlConfigAt(home string) config.TomlConfig {
	tc, err := config.ResolveConfig(e.dataDir, home, "")
	if err != nil {
		return config.TomlConfig{}
	}
	return tc
}

// preflightBlock builds the repo-state block for the current sandbox
// root when preflight_repo is enabled (nil = built-in default ON).
// Returns the block and its token estimate; empty when disabled or when
// the directory yields nothing worth sending.
func (e *Engine) preflightBlock() (string, int) {
	return e.preflightBlockAt(e.Home())
}

func (e *Engine) preflightBlockAt(home string) (string, int) {
	tc := e.tomlConfigAt(home)
	if tc.PreflightRepo != nil && !*tc.PreflightRepo {
		return "", 0
	}
	block := preflight.Build(home, preflight.Options{})
	if block == "" {
		return "", 0
	}
	return block, preflight.EstimateTokens(block)
}

// taskWorkerProvider maps the task_model knob onto a worker backend,
// mirroring the CLI's resolveTaskWorkerConfig: "model-id" keeps the
// coordinator's transport and swaps only the model; "provider/model"
// resolves a configured [[providers]] entry by name. Nil = workers
// inherit the coordinator's provider (task_model unset, unresolvable,
// or naming the coordinator's own backend).
func (e *Engine) taskWorkerProvider(tc config.TomlConfig) (llm.Provider, *config.Config) {
	worker := e.taskWorkerConfig(tc)
	if worker == nil {
		return nil, nil
	}
	return e.buildWorker(*worker)
}

// taskWorkerConfig resolves the task_model knob to a worker config
// WITHOUT building a provider, so the usage sink can label worker
// calls with the right identity cheaply. Nil = workers inherit the
// coordinator's backend.
func (e *Engine) taskWorkerConfig(tc config.TomlConfig) *config.Config {
	tm := strings.TrimSpace(tc.TaskModel)
	if tm == "" {
		return nil
	}
	e.mu.RLock()
	worker := e.cfg
	coordinatorModel := e.cfg.Model
	coordinatorBaseURL := e.cfg.BaseURL
	e.mu.RUnlock()
	if name, model, found := strings.Cut(tm, "/"); found {
		for _, p := range tc.Providers {
			if p.Name != name {
				continue
			}
			worker.Provider = p.Type
			worker.BaseURL = p.BaseURL
			worker.APIKey = p.APIKey
			worker.Model = model
			if worker.Model == "" {
				worker.Model = p.Model
			}
			if worker.Model == "" || (worker.Model == coordinatorModel && worker.BaseURL == coordinatorBaseURL) {
				return nil
			}
			return &worker
		}
	}
	worker.Model = tm
	if worker.Model == coordinatorModel {
		return nil
	}
	return &worker
}

// buildWorker constructs the override provider through the factory
// (metered, purpose "task"), degrading to nil (= inherit the
// coordinator's backend) on any build failure.
func (e *Engine) buildWorker(cfg config.Config) (llm.Provider, *config.Config) {
	if err := cfg.Normalize(); err != nil {
		return nil, nil
	}
	wp, err := e.factory.Build(cfg, llm.PurposeTask)
	if err != nil {
		return nil, nil
	}
	return wp, &cfg
}

// providerManager returns a freshly reloaded provider manager over
// the current SuperCli config.toml. It is cheap and keeps web state in
// sync with changes made by the TUI or by this GUI.
func (e *Engine) providerManager() *providers.Manager {
	m := providers.NewManager(e.dataDir)
	_, projectPath := config.FindTomlPaths(e.dataDir, e.Home())
	m.SetActiveConfigPath(projectPath)
	m.Reload()
	m.LoadHiddenState()
	return m
}

// SwitchModel activates a provider/model pair for future web runs and
// persists the choice ONLY in the web GUI's own state
// (webgui-settings.json), never in the global config.toml. The CLI's
// default_model is deliberately left alone: a model picked in the
// browser must not silently become the CLI default (that is what
// SetCLIDefault is for, as an explicit user action). providerName may
// be empty; in that case the capability registry's provider hint is
// used when possible.
func (e *Engine) SwitchModel(modelID, providerName string) error {
	if modelID == "" {
		return fmt.Errorf("model is empty")
	}
	m := e.providerManager()
	if providerName == "" {
		providerName = e.caps.Provider(modelID)
	}
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	cfg.Model = modelID
	found := false
	for _, pc := range m.Configured() {
		if pc.Name == providerName {
			if pc.Disabled {
				return fmt.Errorf("provider %q is disabled", providerName)
			}
			found = true
			cfg.Provider = pc.Type
			cfg.BaseURL = pc.BaseURL
			cfg.APIKey = pc.APIKey
			break
		}
	}
	if providerName != "" && !found {
		return fmt.Errorf("provider %q is not configured", providerName)
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	tc, _ := config.ResolveConfig(e.dataDir, e.Home(), "")
	prov, err := e.factory.BuildChain(cfg, tc, llm.PurposeMain)
	if err != nil {
		return err
	}
	if err := saveLastModel(e.dataDir, modelID, providerName); err != nil {
		return err
	}
	e.mu.Lock()
	e.cfg = cfg
	e.prov = prov
	e.mu.Unlock()
	return nil
}

// SetCLIDefault persists modelID/providerName as the GLOBAL default
// (default_model/default_provider in config.toml) — the value the CLI
// and TUI start with. This is the explicit, opt-in counterpart to
// SwitchModel: it is only reachable through a dedicated UI action, so
// the web GUI never overwrites the CLI default as a side effect of
// normal browsing.
func (e *Engine) SetCLIDefault(modelID, providerName string) error {
	if modelID == "" {
		return fmt.Errorf("model is empty")
	}
	if providerName == "" {
		providerName = e.caps.Provider(modelID)
	}
	return e.providerManager().SaveActiveConfig(modelID, providerName)
}

// Provider construction moved to internal/llm/factory (factory.Default):
// the web GUI and the CLI now share one construction table, and every
// provider built here comes out metered via Engine.factory.
