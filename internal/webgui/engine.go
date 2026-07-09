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
	"fmt"
	"strings"
	"sync"

	"supercli/internal/account/codexauth"
	"supercli/internal/agent"
	"supercli/internal/llm"
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
	prov, err := buildProviderWithDataDir(cfg, dataDir, caps)
	if err != nil {
		return nil, fmt.Errorf("webgui.NewEngine: provider: %w", err)
	}
	return &Engine{
		cfg:     cfg,
		dataDir: dataDir,
		home:    home,
		caps:    caps,
		prov:    prov,
	}, nil
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
	e.mu.RLock()
	prov := e.prov
	caps := e.caps
	home := e.home
	e.mu.RUnlock()
	reg := tools.NewRegistry()
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
		tools.NewListDir(home).Spec(),
		tools.NewEditLine(home).Spec(),
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
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:        prov,
		Registry:        reg,
		Caps:            caps,
		MaxSteps:        25,
		BaseDir:         home,
		InitialMessages: initial,
		Writer:          writer,
	})
	if err != nil {
		return nil, err
	}
	// Task delegation (mirrors the CLI's runBatch wiring, minus the
	// draft-verify ladder): the web agent can hand self-contained
	// subtasks to fresh workers. A wiring failure degrades to a loop
	// without `task` rather than breaking chat.
	e.wireTaskTool(loop, reg, prov, caps, home)
	return loop, nil
}

// wireTaskTool registers the task / send_message / task_stop tools on
// reg, honouring the config knobs the CLI honours: task_max_steps,
// task_max_tokens, task_model (worker backend override) and
// preflight_repo (cold-context repo briefing for workers).
func (e *Engine) wireTaskTool(loop *agent.Loop, reg *tools.Registry, prov llm.Provider, caps *llm.CapabilityRegistry, home string) {
	subReg := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(subReg, agent.BuiltinSubAgents())
	at, err := agent.NewAgentTool(subReg, loop, reg, prov, caps,
		func(cfg agent.LoopConfig) (*agent.Loop, error) { return agent.NewLoop(cfg) })
	if err != nil {
		return
	}
	tc := e.tomlConfig()
	at.MaxSteps = tc.TaskMaxSteps
	at.MaxTokens = tc.TaskMaxTokens
	if wp := e.taskWorkerProvider(tc); wp != nil {
		at.WorkerProvider = wp
	}
	if tc.PreflightRepo == nil || *tc.PreflightRepo {
		at.Preflight = func() string { return preflight.Build(home, preflight.Options{}) }
	}
	for _, sp := range []tools.Tool{at.Spec(), agent.NewSendMessageTool(at.Workers).Spec(), agent.NewTaskStopTool(at.Workers).Spec()} {
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
}

// tomlConfig returns the merged (global + project) config.toml view,
// degrading to the zero value when nothing is on disk.
func (e *Engine) tomlConfig() config.TomlConfig {
	tc, err := config.ResolveConfig(e.dataDir, e.Home(), "")
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
	tc := e.tomlConfig()
	if tc.PreflightRepo != nil && !*tc.PreflightRepo {
		return "", 0
	}
	block := preflight.Build(e.Home(), preflight.Options{})
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
func (e *Engine) taskWorkerProvider(tc config.TomlConfig) llm.Provider {
	tm := strings.TrimSpace(tc.TaskModel)
	if tm == "" {
		return nil
	}
	e.mu.RLock()
	worker := e.cfg
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
			if worker.Model == "" || (worker.Model == e.cfg.Model && worker.BaseURL == e.cfg.BaseURL) {
				return nil
			}
			return e.buildWorker(worker)
		}
	}
	worker.Model = tm
	if worker.Model == e.cfg.Model {
		return nil
	}
	return e.buildWorker(worker)
}

// buildWorker constructs the override provider, degrading to nil (=
// inherit the coordinator's backend) on any build failure.
func (e *Engine) buildWorker(cfg config.Config) llm.Provider {
	if err := cfg.Normalize(); err != nil {
		return nil
	}
	wp, err := buildProviderWithDataDir(cfg, e.dataDir, e.caps)
	if err != nil {
		return nil
	}
	return wp
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
	prov, err := buildProviderWithDataDir(cfg, e.dataDir, e.caps)
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

// buildProvider mirrors app.buildProvider, which is unexported. It
// maps a config to a concrete llm.Provider. Kept in sync with the
// CLI: echo, opencode, codex, anthropic, or OpenAI-compatible.
func buildProvider(cfg config.Config, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	return buildProviderWithDataDir(cfg, "", caps)
}

func buildProviderWithDataDir(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if cfg.IsEcho() {
		return llm.NewEcho(cfg.Model)
	}
	switch cfg.Provider {
	case config.ProviderOpencode:
		p, err := llm.NewOpencode(llm.OpencodeConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			Capabilities: caps,
		})
		if err != nil {
			return nil, fmt.Errorf("opencode: %w", err)
		}
		// Best-effort model discovery; gateway being down is not fatal.
		_, _ = p.ProbeModels(context.Background())
		return p, nil
	case config.ProviderCodex:
		return buildCodexProvider(cfg, dataDir, caps)
	case config.ProviderAnthropic:
		return llm.NewAnthropic(llm.AnthropicConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			Timeout:      cfg.Timeout,
			Capabilities: caps,
		})
	default:
		return llm.NewOpenAI(llm.OpenAIConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			Timeout:      cfg.Timeout,
			Capabilities: caps,
		})
	}
}

func buildCodexProvider(cfg config.Config, dataDir string, caps *llm.CapabilityRegistry) (llm.Provider, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("codex provider requires SuperCli data dir")
	}
	labels, _ := codexauth.ListAccounts(dataDir)
	var logged []string
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{BackendURL: cfg.BaseURL})
		if mgr.LoggedIn() {
			logged = append(logged, label)
		}
	}
	if len(logged) == 0 {
		mgr := codexauth.NewManager(dataDir, codexauth.Options{BackendURL: cfg.BaseURL})
		if !mgr.LoggedIn() {
			return nil, fmt.Errorf("codex: not logged in — run /login in TUI first")
		}
		logged = []string{codexauth.DefaultAccount}
	}
	pool := make([]llm.Provider, 0, len(logged))
	for _, label := range logged {
		mgr := codexauth.NewManagerFor(dataDir, label, codexauth.Options{BackendURL: cfg.BaseURL})
		info, _ := mgr.Account()
		p, err := llm.NewCodex(llm.CodexConfig{
			BackendURL:   mgr.Options().BackendURL,
			Model:        cfg.Model,
			Tokens:       mgr,
			Timeout:      cfg.Timeout,
			Capabilities: caps,
			DataDir:      dataDir,
			AccountID:    info.AccountID,
		})
		if err != nil {
			return nil, err
		}
		pool = append(pool, p)
	}
	if len(pool) == 1 {
		return pool[0], nil
	}
	return llm.NewRouter(pool...)
}
