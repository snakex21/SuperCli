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
	e.mu.RLock()
	prov := e.prov
	caps := e.caps
	cfg := e.cfg
	appProfile := e.appProfile
	e.mu.RUnlock()
	tc := e.tomlConfigAt(home)
	if strings.EqualFold(strings.TrimSpace(appProfile), "nestcafe") {
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
	discoverer := e.skillDiscovererFor(home)
	skillApplier := tools.NewSkillApplier(discoverer)
	reg.MustRegister(skillApplier.Spec())
	reg.MarkAlwaysOn("apply_skill")
	projectMemory := webMemoryKeeper{dataDir: e.dataDir, home: home}
	globalMemory := webMemoryKeeper{dataDir: e.dataDir, home: home, global: true}
	reg.MustRegister(tools.NewRememberDual(projectMemory, globalMemory).Spec())
	reg.MustRegister(tools.NewRecallDual(projectMemory, globalMemory).Spec())
	reg.MarkAlwaysOn("remember")
	reg.MarkAlwaysOn("recall")
	for _, sp := range []tools.Tool{
		tools.NewReadLines(home).Spec(),
		tools.NewReadContext(home).Spec(),
		tools.NewReadMany(home).Spec(),
		tools.NewReadImage(home, 0).Spec(),
		tools.NewListDir(home).Spec(),
		tools.NewPatchFile(home).Spec(),
		tools.NewCreateFile(home).Spec(),
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
	// Rich attachment readers stay discoverable through tool_search so their
	// larger schemas do not burden ordinary chat turns. The attachment prompt
	// names the exact reader, making selection deterministic.
	for _, sp := range []tools.Tool{
		tools.NewReadPdf(home, 0).Spec(),
		tools.NewReadDocx(home, 0).Spec(),
		tools.NewReadXlsx(home, 0).Spec(),
		tools.NewReadZip(home, 0).Spec(),
	} {
		reg.MustRegister(sp)
	}
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
	contextWindowFor := func(model string) agent.ContextWindowResolution {
		return agent.ResolveContextWindow(model, tc.ContextWindow, 0, caps, e.learned)
	}
	contextProvider, _, _ := e.RuntimeSelection()
	scopedContextWindowFor := func(provider, model string) agent.ContextWindowResolution {
		if tokens, ok := e.modelContexts.Get(provider, model); ok {
			return agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}
		}
		return agent.ContextWindowResolution{}
	}
	systemPrompt := webAgentSystemPrompt(home, e.dataDir, cfg.Model, execProfile.PromptSmall, orchestrator, delegation, goalSvc, appProfile)
	if instructions := llmprompt.ActiveUserInstructions(e.dataDir); instructions != "" {
		systemPrompt += "\n\n" + instructions
	}
	if briefing := webMemoryBriefing(e.dataDir, home, tc.MemoryBriefingTokens); briefing != "" {
		systemPrompt += "\n\n" + briefing
	}
	if folders := folderIndexPrompt(e.dataDir); folders != "" {
		systemPrompt += "\n\n" + folders
	}
	// Branded overlays keep their own adjacent data root. Retain the legacy
	// NestCafe preference key while the overlay migrates to the shared key.
	autoMemory := uiSettingBool(e.dataDir, "supercli.autoMemory",
		uiSettingBool(e.dataDir, "nestcafe.autoMemory", true))
	if autoMemory {
		systemPrompt += "\n\nAfter completing a useful task or learning a durable user preference, call remember with one short self-contained fact. Never store secrets."
	} else {
		systemPrompt += "\n\nDo not proactively save new memory. Only call remember when the user explicitly asks you to remember something."
	}
	maxSteps := tc.MaxStepsOr(25)
	maxStepGrace := 0
	if tc.MaxSteps <= 0 {
		// The built-in WebGUI budget is a safety baseline, not a reason to cut
		// off a healthy long task. Explicit max_steps remains a strict user cap.
		maxStepGrace = maxSteps
	}
	compactProv := e.compactProvider(tc)
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:               prov,
		Registry:               reg,
		Caps:                   caps,
		System:                 systemPrompt,
		MaxSteps:               maxSteps,
		MaxStepGrace:           maxStepGrace,
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
		InitialMessages:        initial,
		Writer:                 writer,
		ContextWindowFor:       contextWindowFor,
		ContextProvider:        contextProvider,
		ScopedContextWindowFor: scopedContextWindowFor,
		Summarizer:             agent.NewAutoSummarizerWithProvider(compactProv, reg.ActiveNames),
		LearnLimit:             e.learned.Learn,
		// Zero-LLM tool-result prune (first line of context defense,
		// before the summary fallback). Config prune_protect_tokens:
		// 0 = scale with the active model window, negative = off.
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
