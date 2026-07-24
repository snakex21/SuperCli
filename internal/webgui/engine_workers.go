// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"fmt"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
	"supercli/internal/system/preflight"
	"supercli/internal/tools"
)

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
	e.mu.RLock()
	officeProfile := strings.EqualFold(strings.TrimSpace(e.appProfile), "nestcafe")
	e.mu.RUnlock()
	if officeProfile {
		return "", 0
	}
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

func (e *Engine) compactProvider(tc config.TomlConfig) llm.Provider {
	cfg := e.modelOverrideConfig(tc, tc.CompactModel)
	if cfg == nil {
		return nil
	}
	p, err := e.factory.Build(*cfg, llm.PurposeCompact)
	if err != nil {
		return nil
	}
	return p
}

// taskWorkerConfig resolves the task_model knob to a worker config
// WITHOUT building a provider, so the usage sink can label worker
// calls with the right identity cheaply. Nil = workers inherit the
// coordinator's backend.
func (e *Engine) taskWorkerConfig(tc config.TomlConfig) *config.Config {
	return e.modelOverrideConfig(tc, tc.TaskModel)
}

func (e *Engine) modelOverrideConfig(tc config.TomlConfig, ref string) *config.Config {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	e.mu.RLock()
	worker := e.cfg
	coordinatorModel := e.cfg.Model
	coordinatorBaseURL := e.cfg.BaseURL
	e.mu.RUnlock()
	if name, model, found := strings.Cut(ref, "/"); found {
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
	worker.Model = ref
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
