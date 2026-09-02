package app

import (
	"context"
	"log"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/system/preflight"
	"supercli/internal/tools"
)

// agentToolWiring is the config needed to build and register the
// coordinator's task/send_message/task_stop tools.
type agentToolWiring struct {
	loop               *agent.Loop
	registry           *tools.Registry
	provider           llm.Provider
	caps               *llm.CapabilityRegistry
	tomlCfg            config.TomlConfig
	taskWorkerProvider llm.Provider
	taskWorkerCfg      config.Config
	home               string
	delegationOff      bool
	coordinatorMode    bool
}

// wireAgentTool builds AgentTool, applies task limits / draft-verify /
// preflight, and registers delegation tools when allowed.
func wireAgentTool(w agentToolWiring) (*agent.AgentTool, error) {
	subReg := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(subReg, agent.BuiltinSubAgents())
	at, err := agent.NewAgentTool(
		subReg,
		w.loop,
		w.registry,
		w.provider,
		w.caps,
		func(cfg agent.LoopConfig) (*agent.Loop, error) {
			return agent.NewLoop(cfg)
		},
	)
	if err != nil {
		return nil, err
	}
	// Worker resource limits (config `task_max_steps` / `task_max_tokens`,
	// both optional). Built-in workers leave their step budget unset, so an
	// explicit task_max_steps applies to every kind; zero uses the shared high
	// runaway safety net. Tokens remain a hard cap (0 = no cap).
	at.MaxSteps = w.tomlCfg.TaskMaxSteps
	at.MaxTokens = w.tomlCfg.TaskMaxTokens
	// Model-per-task: hand the worker backend (built above from
	// `task_model`) to the task tool. The lazy ping (first delegation
	// only, 5s cap) is a GET /v1/models probe — cheap and universal for
	// OpenAI-compat hosts; anthropic/codex transports skip it (their
	// model lists need different auth) and just trust the build.
	if w.taskWorkerProvider != nil {
		at.WorkerProvider = w.taskWorkerProvider
		at.WorkerContextProvider = config.RuntimeProviderName(w.tomlCfg, w.taskWorkerCfg)
		if u := w.taskWorkerCfg.BaseURL; u != "" &&
			w.taskWorkerCfg.Provider != config.ProviderAnthropic &&
			w.taskWorkerCfg.Provider != config.ProviderCodex {
			key := w.taskWorkerCfg.APIKey
			at.WorkerPing = func(pctx context.Context) error {
				_, pingErr := llm.ListProviderModelContexts(pctx, u, key)
				return pingErr
			}
		}
	}
	if w.loop != nil {
		at.PrefillProfiles = w.loop.PrefillProfiles()
	}
	// Draft-verify ladder (config `draft_verify`, tri-state, default OFF).
	// When on, a file-changing draft is sieved by verify_commands (free,
	// objective) and then judged by the COORDINATOR's model (the big one,
	// `provider`) on the diff + evidence. Nil/false = the task tool is
	// byte-identical to before. The verdict runs on the coordinator's
	// provider even when workers use task_model — that asymmetry (small
	// drafts, big verdict) is the whole point.
	if resolveDraftVerify(w.tomlCfg.DraftVerify) {
		at.DraftVerify = &agent.DraftVerifyConfig{
			Enabled:        true,
			VerifyCommands: w.tomlCfg.VerifyCommands,
			MaxRounds:      w.tomlCfg.DraftVerifyMaxRounds,
			Verdict:        w.provider,
		}
		log.Printf("draft-verify: ON · verify_commands=%v · max_rounds=%d · verdict=%s",
			w.tomlCfg.VerifyCommands, w.tomlCfg.DraftVerifyMaxRounds, w.provider.Name())
	}
	// Preflight repo context (config `preflight_repo`, tri-state, default
	// ON). When on, a compact repo-state block (hard token budget) is
	// appended ONCE to the first user message — the variable side of the
	// prompt, never the system prefix, so the KV-cache front stays stable
	// — and freshly rebuilt for every delegated worker's briefing (cold
	// contexts benefit most). Cost is visible in the normal per-turn
	// token telemetry; the one-line log states the estimate up front.
	if resolvePreflightRepo(w.tomlCfg.PreflightRepo) {
		if block := preflight.Build(w.home, preflight.Options{}); block != "" {
			w.loop.SetNextCoordinatorAddon(block)
			log.Printf("preflight: repo context ~%d tok (rides the first user message)",
				preflight.EstimateTokens(block))
		}
		home := w.home
		at.Preflight = func() string { return preflight.Build(home, preflight.Options{}) }
	}
	if !w.delegationOff {
		w.registry.MustRegister(at.Spec())
		sendMessageTool := agent.NewSendMessageTool(at.Workers)
		w.registry.MustRegister(sendMessageTool.Spec())
		taskStopTool := agent.NewTaskStopTool(at.Workers)
		w.registry.MustRegister(taskStopTool.Spec())
		if w.coordinatorMode {
			w.registry.MarkAlwaysOn("task")
			w.registry.MarkAlwaysOn("send_message")
			w.registry.MarkAlwaysOn("task_stop")
		}
	}
	// F14: opt-in tool. The model calls hide_messages
	// when it wants to drop old messages from its own
	// context. We bind it after NewLoop because the tool
	// needs the loop as Hider and a way to ask its
	// current Messages length.
	hideTool := tools.NewHideMessages(w.loop, func() int {
		return len(w.loop.AllMessages())
	})
	w.registry.MustRegister(hideTool.Spec())
	return at, nil
}
