package app

import (
	"context"
	"log"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/agent"
	"supercli/internal/llm/prompt"
	"supercli/internal/storage/freshness"
	"supercli/internal/storage/goal"
	"supercli/internal/system/config"
)

// codexAuthMgr handles ChatGPT-subscription (Codex) OAuth tokens.
// Set once at startup from the [codex_auth] config section;
// buildProvider and the /login and /logout commands use it.
var codexAuthMgr *codexauth.Manager

// initCodexAuth builds the codex auth manager from config.
func initCodexAuth(dataDir string, t config.TomlConfig) {
	codexAuthMgr = codexauth.NewManager(dataDir, codexauth.Options{
		ClientID:   t.CodexAuth.ClientID,
		Issuer:     t.CodexAuth.Issuer,
		BackendURL: t.CodexAuth.BackendURL,
	})
}

// supercliSystemPromptBase is the layered system prompt
// shared by the main loop and any F6 Darwin children. It
// defaults to core + extended guidance; main() rebuilds it
// once the model's tier is known — small-tier models get the
// core only (see internal/prompt and internal/tier).
var supercliSystemPromptBase = prompt.Build(false)
var supercliModelProfile string
var supercliUserInstructions string
var supercliCoordinatorMode bool

// supercliOrchestratorMode is the HARD delegation mode (explicit
// `orchestrator = true`). When true the main loop runs with a
// restricted registry (agent.OrchestratorRegistry): delegation + a
// read-only lookup set only, so the coordinator physically cannot edit
// files or run commands and must delegate via `task`. Resolved once in
// main() from config; /orchestrator persists the change for next launch.
var supercliOrchestratorMode bool

// supercliDelegationDisabled is the explicit `orchestrator = false` state.
// Unlike the nil/default adaptive state, it removes task/send_message/task_stop
// from the main agent entirely, so "never" is a real capability boundary and
// not merely a prompt suggestion.
var supercliDelegationDisabled bool

// memoryBriefing is the code-built session-start briefing (user
// preferences, project card, recent session summaries, other
// projects). Set once in main() before the loop is created.
var memoryBriefing string

// workingDirNote states the ACTUAL sandbox root (the BaseDir the
// file tools enforce) so the model uses the right path on its
// first file call. Derived in main() from the same resolved home
// the tools get — never hardcoded — and injected last so it wins
// over any conflicting project path a memory fact might mention.
var workingDirNote string

// memoryAutoSaveInstruction backs the B4 contract: the model is
// told to save a task-log entry after each finished task; the
// AutoSaver in code covers sessions where it forgets.
const memoryAutoSaveInstruction = "Memory: after completing a task, call remember with " +
	"type=task-log summarizing WHAT you did, WHY, and which files you touched. " +
	"Save user preferences with type=preference (scope=global). Use recall at the " +
	"start of non-trivial tasks to check prior context."

// buildSystemPrompt returns the base prompt plus the
// current ISO date stamp and, if a goal service is
// passed and has an active goal, the [current_goal]
// block listing the title and pending tasks.
//
// F8: goal injection lives here so the main agent and
// any Darwin children see the same active goal.
func buildSystemPrompt(svc *goal.Service) string {
	base := supercliSystemPromptBase + "\n\n" + freshness.PromptSection(time.Now()) + "\n" + platformHint()
	if supercliModelProfile != "" {
		base += "\n\n" + supercliModelProfile
	}
	if supercliUserInstructions != "" {
		base += "\n\n" + supercliUserInstructions
	}
	// Orchestrator mode is a stricter coordinator: its lean preamble
	// replaces the coordinator section (it subsumes the delegate-first
	// guidance and adds the hard "you have no edit/run tools" boundary).
	if supercliOrchestratorMode {
		base += agent.OrchestratorPrompt()
	} else if supercliCoordinatorMode {
		base += agent.CoordinatorPrompt()
	}
	if memoryBriefing != "" {
		base += "\n\n" + memoryBriefing
	}
	// Inject AFTER the briefing so the real sandbox root wins over
	// any conflicting project path a memory fact may carry.
	if workingDirNote != "" {
		base += "\n\n" + workingDirNote
	}
	base += "\n\n" + memoryAutoSaveInstruction
	if svc == nil {
		return base
	}
	injected, err := svc.Inject(context.Background(), base, 5)
	if err != nil {
		log.Printf("goal inject: %v", err)
		return base
	}
	return injected
}
