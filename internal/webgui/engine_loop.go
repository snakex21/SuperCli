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
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/system/execution"
	systats "supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
	"supercli/internal/tools/mcp"
)

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
	if _, err := goalSvc.Refresh(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("refresh goal: %w", err)
	}
	reg := tools.NewRegistry()
	// Completed tool envelopes are omitted from future provider prompts, while
	// their full persisted rows remain retrievable on demand through this local
	// FTS tool. It is discoverable rather than always-on, so it adds no schema
	// tokens to ordinary turns.
	if history, historyErr := e.sessionStore(); historyErr == nil {
		reg.MustRegister(tools.NewSearchHistory(history).Spec())
	}
	// Registered but not always-on: thin discovery keeps the LSP schema out of
	// ordinary chat turns, while the Engine reuses the lazy server per project.
	codeIntel := e.codeIntelFor(home)
	reg.MustRegister(codeIntel.Spec())
	reg.MustRegister(e.processSessionFor(home).Spec())
	reg.MustRegister(tools.NewHeadlessControl(home, e.DataDir()).Spec())
	discoverer := e.skillDiscovererFor(home)
	skillApplier := tools.NewSkillApplier(discoverer)
	reg.MustRegister(skillApplier.Spec())
	reg.MarkAlwaysOn("apply_skill")
	projectMemory := webMemoryKeeper{engine: e, home: home}
	globalMemory := webMemoryKeeper{engine: e, home: home, global: true}
	reg.MustRegister(tools.NewRememberDual(projectMemory, globalMemory).Spec())
	reg.MustRegister(tools.NewRecallDual(projectMemory, globalMemory).Spec())
	reg.MarkAlwaysOn("remember")
	reg.MarkAlwaysOn("recall")
	// One edit path, matching agent.thinCoreTools in the TUI: patch_file
	// to change a file, create_file to make one. Offering the model seven
	// interchangeable editors is what made it pick the wrong one (and reach
	// for write_file on a Word document); the line editors are gone entirely
	// now, and patch_file absorbed the ergonomics they were kept for.
	ctxTool := tools.NewCtxExecuteTool(ctxexec.New(home), home)
	ctxTool.NativeOfficeOnly = officeProfile
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
		tools.NewReadMany(home).Spec(),
		tools.NewReadImage(home, 0).Spec(),
		tools.NewListDir(home).Spec(),
		tools.NewPatchFile(home).Spec(),
		tools.NewCreateFile(home).Spec(),
		tools.NewMakeDir(home).Spec(),
		tools.NewMove(home).Spec(),
		tools.NewCopy(home).Spec(),
		tools.NewTrash(home).Spec(),
		tools.NewSearchCode(home).Spec(),
		ctxTool.Spec(),
		tools.NewScratchpad(home).Spec(),
	} {
		if turn != nil {
			sp = turn.Wrap(sp)
		}
		sp = codeIntel.WrapMutation(sp)
		reg.MustRegister(sp)
		reg.MarkAlwaysOn(sp.Name)
	}
	// write_file overwrites a whole file and stays discoverable rather than
	// core: patch_file changes a file, create_file makes one, and a full
	// rewrite is rare enough to be worth a tool_search.
	for _, sp := range []tools.Tool{
		tools.NewWriteFile(home).Spec(),
	} {
		if turn != nil {
			sp = turn.Wrap(sp)
		}
		sp = codeIntel.WrapMutation(sp)
		reg.MustRegister(sp)
	}
	// Rich attachment readers stay discoverable through tool_search so their
	// larger schemas do not burden ordinary chat turns. The attachment prompt
	// names the exact reader, making selection deterministic.
	for _, sp := range []tools.Tool{
		tools.NewReadPdf(home, 0).Spec(),
		tools.NewReadDocx(home, 0).Spec(),
		tools.NewReadXlsx(home, 0).Spec(),
		tools.NewSendScreenshot(e.DataDir(), nil).Spec(),
		tools.NewShowMedia(home).Spec(),
	} {
		reg.MustRegister(sp)
	}
	zipSpec := tools.NewReadZip(home, 0).Spec()
	if turn != nil {
		zipSpec = turn.Wrap(zipSpec)
	}
	reg.MustRegister(zipSpec)
	e.registerMediaGeneration(reg, home)
	// Office editors are discoverable alongside their readers. Keeping them
	// out of the always-on set avoids schema overhead in ordinary chat, while
	// tool_search can expose them for an explicit Word or Excel request.
	for _, sp := range []tools.Tool{
		tools.NewEditDocx(home).Spec(),
		tools.NewEditXlsx(home).Spec(),
	} {
		if turn != nil {
			sp = turn.Wrap(sp)
		}
		reg.MustRegister(sp)
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
		// A web/desktop question is an explicit pause in the foreground run.
		// Do not let the coordinator silently continue while the user is away
		// from the window; the request/run context still cancels immediately on
		// Stop, interrupt, disconnect or shutdown.
		ask.Timeout = 24 * time.Hour
		reg.MustRegister(ask.Spec())
		reg.MarkAlwaysOn("ask_user")
	}
	// Current facts should not fall through to browser automation. Keep one
	// tiny query-only tool always available, while the larger filtered search
	// and fetch schemas remain discoverable through tool_search.
	reg.MustRegister(tools.NewWebFetch().Spec())
	downloadSpec := tools.NewWebDownload(home).Spec()
	if turn != nil {
		downloadSpec = turn.Wrap(downloadSpec)
	}
	reg.MustRegister(codeIntel.WrapMutation(downloadSpec))
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
	// Desktop Outlook COM is available to NestCafe/WebUI too. Keep it
	// discoverable rather than always-on so ordinary chats pay no schema cost.
	reg.MustRegister(tools.NewOutlookMail().Spec())
	// Direct Thunderbird bridge for Gmail/IMAP verification. Read-only for now;
	// it is discoverable and therefore costs nothing in unrelated turns.
	reg.MustRegister(tools.NewThunderbirdMail(e.DataDir()).Spec())
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
	contextWindowFor := func(model string) agent.ContextWindowResolution {
		return agent.ResolveContextWindowWithRuntime(model, tc.ContextWindow, 0, caps, e.learned, cfg.BaseURL, cfg.APIKey)
	}
	configuredProviders := e.providerManager().Configured()
	contextProvider := runtimeProviderForConfig(cfg, caps, configuredProviders)
	configForProvider := contextWindowConfigForProvider(cfg, contextProvider, configuredProviders)
	scopedContextWindowFor := func(provider, model string) agent.ContextWindowResolution {
		scopedCfg := configForProvider(provider)
		if tokens, ok := e.modelContexts.Get(provider, model); ok {
			return agent.ClampContextWindowToRuntime(agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}, scopedCfg.BaseURL, scopedCfg.APIKey, model)
		}
		resolved := agent.ResolveContextWindowWithRuntime(model, tc.ContextWindow, 0, caps, e.learned, scopedCfg.BaseURL, scopedCfg.APIKey)
		if resolved.Tokens <= 0 {
			return agent.ContextWindowResolution{Tokens: agent.DefaultContextWindow(), Source: "fallback"}
		}
		return resolved
	}
	systemPrompt := webAgentSystemPrompt(home, e.dataDir, cfg.Model, execProfile.PromptSmall, orchestrator, delegation, nil, appProfile)
	if instructions := llmprompt.ActiveUserInstructions(e.dataDir); instructions != "" {
		systemPrompt += "\n\n" + instructions
	}
	currentSessionID := ""
	if bound, ok := writer.(interface{ SessionID() string }); ok {
		currentSessionID = bound.SessionID()
	}
	// These snapshots may change after any turn. Keep the latest values
	// available without rewriting the beginning of the conversation.
	liveContext := e.webMemoryBriefingExcludingSession(home, tc.MemoryBriefingTokens, currentSessionID)
	if folders := folderIndexPrompt(e.dataDir); folders != "" {
		if liveContext != "" {
			liveContext += "\n\n"
		}
		liveContext += folders
	}
	if current, injectErr := goalSvc.Inject(context.Background(), liveContext, 5); injectErr == nil {
		liveContext = current
	} else {
		return nil, nil, fmt.Errorf("goal context: %w", injectErr)
	}
	// Branded overlays keep their own adjacent data root. Retain the legacy
	// NestCafe preference key while the overlay migrates to the shared key.
	autoMemory := uiSettingBool(e.dataDir, "supercli.autoMemory", true, "nestcafe.autoMemory")
	systemPrompt += "\n\n" + llmprompt.MemoryGuidance(autoMemory)
	// One shared runaway safety net (agent.DefaultMaxSteps) on every surface.
	// An explicit max_steps in config.toml stays a strict user cap.
	maxSteps := tc.MaxStepsOr(agent.DefaultMaxSteps)
	compactProv := e.compactProvider(tc)
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:               prov,
		Registry:               reg,
		Caps:                   caps,
		System:                 systemPrompt,
		LiveContext:            liveContext,
		MaxSteps:               maxSteps,
		Orchestrator:           orchestrator,
		TaskParallel:           taskParallel,
		TaskParallelWarnLocal:  taskParallelWarnLocal,
		EnableNavigator:        execProfile.EnableNavigator,
		NavigatorAuto:          execProfile.NavigatorAuto,
		NavigatorKeywordsOnly:  execProfile.NavigatorKeywordsOnly,
		ThinTools:              execProfile.ThinTools,
		StableToolset:          execProfile.StableToolset,
		CatalogHoist:           execProfile.CatalogHoist,
		BaseDir:                home,
		UserDownloadsDir:       agent.SystemDownloadsDir,
		InitialMessages:        initial,
		Writer:                 writer,
		ContextWindowFor:       contextWindowFor,
		ContextProvider:        contextProvider,
		ScopedContextWindowFor: scopedContextWindowFor,
		Summarizer:             agent.NewAutoSummarizerWithProvider(compactProv, reg.ActiveNames),
		LearnLimit:             e.learned.Learn,
		PrefillProfiles:        e.prefillProfiles,
		// Zero-LLM tool-result prune (first line of context defense,
		// before the summary fallback). Config prune_protect_tokens:
		// 0 = scale with the active model window, negative = off.
		PruneProtectTokens: tc.PruneProtectTokens,
		Stats:              recorder,
		// Attributed tool failures go to <dataDir>/logs/tool_errors.log,
		// exactly as in the CLI. The web front-end shipped without this
		// wiring, so every failure since the GUI became the primary
		// front-end was classified and then thrown away.
		ErrorLog: e.toolErrorLog(),
	})
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
	e.diagnosticMu.Lock()
	e.toolDiagnostics = reg.Diagnostics()
	e.diagnosticMu.Unlock()
	if orchestrator {
		// Workers retain the complete base registry above; only the parent loop
		// is physically restricted to delegation and read-only lookup tools.
		loop.SetRegistry(agent.OrchestratorRegistry(reg))
	}
	return loop, reg, nil
}
