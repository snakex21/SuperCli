// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"supercli/internal/account/pricing"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/llm/providers"
	"supercli/internal/storage"
	"supercli/internal/system/config"
	"supercli/internal/system/preflight"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
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
	learned *llm.LearnedLimits
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
	prov, err := f.Build(cfg, llm.PurposeMain)
	if err != nil {
		return nil, fmt.Errorf("webgui.NewEngine: provider: %w", err)
	}
	eng := &Engine{
		cfg:     cfg,
		dataDir: dataDir,
		home:    home,
		caps:    caps,
		prov:    prov,
		factory: f,
		learned: llm.LoadLearnedLimits(dataDir),
	}
	eng.titles = newTitleScheduler(titleIdleDelay, eng.runSessionTitleLLM)
	eng.providerManager().SetModelPrices(caps)
	return eng, nil
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
	e.mu.RLock()
	prov := e.prov
	caps := e.caps
	cfg := e.cfg
	e.mu.RUnlock()
	tc := e.tomlConfigAt(home)
	orchestrator := tc.Orchestrator != nil && *tc.Orchestrator
	taskParallel := !llm.IsLocalBaseURL(cfg.BaseURL)
	if tc.TaskParallel != nil {
		taskParallel = *tc.TaskParallel
	}
	reg := tools.NewRegistry()
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
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
	} {
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
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
		Provider:        prov,
		Registry:        reg,
		Caps:            caps,
		System:          webAgentSystemPrompt(home, orchestrator),
		MaxSteps:        25,
		Orchestrator:    orchestrator,
		TaskParallel:    taskParallel,
		BaseDir:         home,
		InitialMessages: initial,
		Writer:          writer,
		WindowFor:       windowFor,
		Summarizer:      agent.NewAutoSummarizer(reg.ActiveNames),
		LearnLimit:      e.learned.Learn,
		// Zero-LLM tool-result prune (first line of context defense,
		// before the summary fallback). Config prune_protect_tokens:
		// 0 = default 8192-token protected tail, negative = off.
		PruneProtectTokens: tc.PruneProtectTokens,
	})
	if err != nil {
		return nil, err
	}
	// Task delegation (mirrors the CLI's runBatch wiring, minus the
	// draft-verify ladder): the web agent can hand self-contained
	// subtasks to fresh workers. A wiring failure degrades to a loop
	// without `task` rather than breaking chat.
	if err := e.wireTaskTool(loop, reg, prov, caps, home, tc); err != nil {
		return nil, err
	}
	if orchestrator {
		// Workers retain the complete base registry above; only the parent loop
		// is physically restricted to delegation and read-only lookup tools.
		loop.SetRegistry(agent.OrchestratorRegistry(reg))
	}
	return loop, nil
}

func webAgentSystemPrompt(home string, orchestrator bool) string {
	system := llmprompt.Build(false)
	if orchestrator {
		system += agent.OrchestratorPrompt()
	} else {
		system += agent.CoordinatorPrompt()
	}
	// An HTTP/SSE run ends with the parent turn. Background task notifications
	// cannot be delivered to a closed response, so web coordinators deliberately
	// use synchronous task calls.
	system += "\n\nWeb GUI: call task synchronously; do not request async/background workers."
	system += fmt.Sprintf("\n\nActive workspace (all file and shell tools are sandboxed here): %s", home)
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
	cfg := e.cfg
	cfg.Model = modelID
	for _, pc := range m.Configured() {
		if pc.Name == providerName {
			cfg.Provider = pc.Type
			cfg.BaseURL = pc.BaseURL
			cfg.APIKey = pc.APIKey
			break
		}
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	prov, err := e.factory.Build(cfg, llm.PurposeMain)
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
