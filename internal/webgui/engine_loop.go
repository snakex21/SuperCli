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
	"fmt"
	"os"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/assembly"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	systats "supercli/internal/system/stats"
	"supercli/internal/tools"
)

// observeLoopConfig is a test-only seam: golden tests capture the exact
// LoopConfig a web loop is built from. Always nil in production.
var observeLoopConfig func(agent.LoopConfig)

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
	loop, _, err := e.buildLoopWithSession(initial, writer, home, askCh, turn, telemetry...)
	return loop, err
}

// buildLoopWithSession returns the complete base registry to callers wiring a
// run. Engine retains only its diagnostic counts; the loop owns executable tools.
func (e *Engine) buildLoopWithSession(initial []llm.Message, writer agent.SessionWriter, home string, askCh chan<- tools.AskRequest, turn *checkpoint.Turn, telemetry ...systats.Recorder) (*agent.Loop, *tools.Registry, error) {
	e.mu.RLock()
	prov := e.prov
	caps := e.caps
	cfg := e.cfg
	appProfile := e.appProfile
	e.mu.RUnlock()
	officeProfile := strings.EqualFold(strings.TrimSpace(appProfile), "nestcafe")
	tc := e.tomlConfigAt(home)
	if officeProfile {
		// Repository preflight is useful in SuperCli, but it primes an office
		// assistant to treat every folder as a source-code project. NestCafe
		// keeps a technical working directory internally, without exposing its
		// git/repository state to the model on ordinary document tasks.
		off := false
		tc.PreflightRepo = &off
	}
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
	goalSvc, err := e.goalServiceAt(context.Background(), home)
	if err != nil {
		return nil, nil, fmt.Errorf("goal service: %w", err)
	}
	// The system prompt is assembled first: darwin candidates inherit it.
	systemPrompt, projectInstructions := webAgentSystemPromptLayers(home, e.dataDir, cfg.Model, execProfile.PromptSmall, orchestrator, delegation, nil, appProfile)
	// Branded overlays keep their own adjacent data root. Retain the legacy
	// NestCafe preference key while the overlay migrates to the shared key.
	// [memory] enabled (global or project config.toml), SUPERCLI_MEMORY and
	// --no-memory are resolved once per run. Off: no briefing, no recall, no
	// remember/recall tools and no automatic saves; stored data is untouched.
	memoryOn, _ := config.EffectiveMemory(tc)
	if memoryOn {
		autoMemory := uiSettingBool(e.dataDir, "supercli.autoMemory", true, "nestcafe.autoMemory")
		systemPrompt += "\n\n" + llmprompt.MemoryGuidance(autoMemory)
	} else {
		systemPrompt = llmprompt.WithoutMemoryHints(systemPrompt) + "\n\n" + llmprompt.MemoryOffGuidance
	}

	reg := tools.NewRegistry()
	provided := map[string]tools.Tool{}
	if memoryOn {
		projectMemory := webMemoryKeeper{engine: e, home: home}
		globalMemory := webMemoryKeeper{engine: e, home: home, global: true}
		provided["remember"] = tools.NewRememberDual(projectMemory, globalMemory).Spec()
		provided["recall"] = tools.NewRecallDual(projectMemory, globalMemory).Spec()
	}
	if history, historyErr := e.sessionStore(); historyErr == nil {
		searchHistory := tools.NewSearchHistory(history)
		if !memoryOn {
			// Memory off: only this conversation stays searchable (compaction
			// recovery); earlier conversations are out of reach.
			runSessionID := ""
			if bound, ok := writer.(interface{ SessionID() string }); ok {
				runSessionID = bound.SessionID()
			}
			searchHistory.CurrentOnly = true
			searchHistory.Scope = func(context.Context) (string, bool) { return runSessionID, true }
		}
		provided["search_history"] = searchHistory.Spec()
		recallSessionID := ""
		if bound, ok := writer.(interface{ SessionID() string }); ok {
			recallSessionID = bound.SessionID()
		}
		provided["session_recall"] = (&tools.SessionRecall{
			Store:   history,
			Session: func(context.Context) string { return recallSessionID },
			Worker:  agent.WorkerRecallTranscript,
		}).Spec()
	}
	if !officeProfile && config.DarwinEnabled(tc) {
		provided["darwin"] = e.darwinSpec(reg, prov, cfg, tc, home, systemPrompt)
	}
	// Registered but not always-on: thin discovery keeps the LSP schema out of
	// ordinary chat turns, while the Engine reuses the lazy server per project.
	codeIntel := e.codeIntelFor(home)
	deps := &assembly.Deps{
		Home:           home,
		DataDir:        e.DataDir(),
		Toml:           tc,
		Registry:       reg,
		CodeIntel:      codeIntel,
		ProcessSession: e.processSessionFor(home),
		Skills:         e.skillDiscovererFor(home),
		// Only the human-facing stream has an interactive channel. A web
		// question is an explicit pause in the foreground run: Stop,
		// interrupt, disconnect or shutdown still cancel it immediately.
		AskCh:      askCh,
		AskTimeout: 24 * time.Hour,
		Browser:    e.sharedBrowser(),
		OfficeOnly: officeProfile,
		Goals:      goalSvc,
		MCP:        e.mcpRuntimeFor(home),
		Provided:   provided,
	}
	if turn != nil {
		deps.Checkpoint = turn.Wrap
	}
	assembly.Register(reg, assembly.Web, deps)
	// Context defense (shared with the CLI): without a window resolver the
	// loop assumes its 16384-token default, and without a summarizer
	// auto-compaction degrades to the blind hide fallback.
	resolveWindow := func(model string, c config.Config) agent.ContextWindowResolution {
		return agent.ResolveContextWindowWithRuntime(model, tc.ContextWindow, 0, caps, e.learned, c.BaseURL, c.APIKey)
	}
	configuredProviders := e.providerManager().Configured()
	contextProvider := runtimeProviderForConfig(cfg, caps, configuredProviders)
	configForProvider := contextWindowConfigForProvider(cfg, contextProvider, configuredProviders)
	instructionsDataDir := e.dataDir
	currentSessionID := ""
	if bound, ok := writer.(interface{ SessionID() string }); ok {
		currentSessionID = bound.SessionID()
	}
	// Pin the root and session, but refresh their state at safe model boundaries.
	// Memory changes and goal completion within this run must not retain the
	// snapshot from before the tool executed.
	liveContextBlocks := e.webLiveContextBlocksMemory(home, tc.MemoryBriefingTokens, currentSessionID, goalSvc, officeProfile, memoryOn)
	compactResolver := e.compactProviderResolver(tc, cfg, prov)
	loopCfg := assembly.BuildLoopConfig(assembly.LoopSpec{
		Provider:            prov,
		Registry:            reg,
		Caps:                caps,
		System:              systemPrompt,
		ProjectInstructions: projectInstructions,
		UserInstructions:    llmprompt.ActiveUserInstructions(instructionsDataDir),
		UserInstructionsForRun: func(ctx context.Context) (string, error) {
			return llmprompt.UserInstructionsForRun(ctx, instructionsDataDir)
		},
		LiveContextBlocks: func(ctx context.Context) ([]llm.ContextBlock, error) {
			blocks, err := liveContextBlocks(ctx)
			if err == nil && goalSvc.Active() != nil {
				// Refresh once at the safe request boundary, then activate the
				// goal schema before Run freezes native/thin tool definitions.
				reg.MarkAlwaysOn("goal")
			}
			return blocks, err
		},
		Toml:                   tc,
		Profile:                execProfile,
		Orchestrator:           orchestrator,
		TaskParallel:           taskParallel,
		TaskParallelWarnLocal:  taskParallelWarnLocal,
		Home:                   home,
		InitialMessages:        initial,
		Writer:                 writer,
		ContextWindowFor:       func(model string) agent.ContextWindowResolution { return resolveWindow(model, cfg) },
		ContextProvider:        contextProvider,
		ScopedContextWindowFor: assembly.ScopedContextWindow(e.modelContexts, configForProvider, resolveWindow),
		Summarizer:             agent.NewAutoSummarizerWithProviderResolver(compactResolver, reg.ActiveNames),
		LearnLimit:             e.learned.Learn,
		PrefillProfiles:        e.prefillProfiles,
		Stats:                  recorder,
		// Attributed tool failures go to <dataDir>/logs/tool_errors.log, as in the CLI.
		ErrorLog: e.toolErrorLog(),
		// review_model: interactive post-work review ("Sprawdzanie"); nil = off.
		Review: e.postReviewConfig(tc),
	})
	if observeLoopConfig != nil {
		observeLoopConfig(loopCfg)
	}
	loop, err := agent.NewLoop(loopCfg)
	if err != nil {
		return nil, nil, err
	}
	if delegation {
		// AUTO and ON expose workers. Explicit OFF skips this block, so task,
		// send_message and task_stop cannot be discovered or executed.
		if err := e.wireTaskTool(loop, reg, prov, caps, home, tc, turn); err != nil {
			return nil, nil, err
		}
	}
	if askCh != nil {
		loop.ConfigureHostPower(e.hostPowerController(), goalSvc, true)
	}
	e.diagnosticMu.Lock()
	e.toolDiagnostics = reg.Diagnostics()
	e.diagnosticMu.Unlock()
	if orchestrator {
		// Workers retain the complete base registry above; only the parent loop
		// is physically restricted to delegation and read-only lookup tools.
		loop.SetRegistry(assembly.OrchestratorRegistry(reg, askCh != nil))
	}
	return loop, reg, nil
}
