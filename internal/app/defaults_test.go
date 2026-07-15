// Fresh-install defaults audit (2026-07-10). A brand-new SuperCli —
// empty config.toml, every knob at its zero value — must resolve to
// the BEST configuration for the typical setup (one local model, one
// host). This test pins that contract in ONE place: any future
// default change has to edit a line here, i.e. be a conscious
// decision, never an accident.
//
// Deliberately OFF/empty by default (opt-in features for a conscious
// choice, not pure wins): hard orchestration (adaptive delegation remains
// available), draft_verify + task_model (only pay off with
// a second/smaller model), noop_gate (would change ANSWER semantics:
// a repeated identical batch QUESTION would return "no-op" instead of
// the answer — see the table below).
package app

import (
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// TestFreshConfigDefaults resolves every tri-state/switch knob from a
// zero-value TomlConfig exactly the way Main() does and asserts the
// expected fresh-install behaviour.
func TestFreshConfigDefaults(t *testing.T) {
	fresh := config.TomlConfig{}
	if mode := resolveOrchestratorMode(fresh.Orchestrator); mode != orchestratorAdaptive {
		t.Fatalf("fresh orchestrator mode = %v, want adaptive", mode)
	}

	navEnable, navAuto := resolveNavigator(fresh.Navigator)
	bools := []struct {
		knob string
		got  bool
		want bool
		why  string
	}{
		{"stable_toolset", resolveStableToolset(fresh.StableToolset), true,
			"fixed tools list preserves the KV prompt cache; live-confirmed"},
		{"preflight_repo", resolvePreflightRepo(fresh.PreflightRepo), true,
			"~73 tok buys −33% turns/−42% tokens; same result, git optional"},
		{"noop_gate", resolveNoopGate(fresh.NoopGate), false,
			"gating repeated identical batch QUESTIONS would swallow the answer"},
		{"orchestrator", resolveOrchestrator(fresh.Orchestrator), false,
			"hard delegation is an opt-in working mode, not a default"},
		{"draft_verify", resolveDraftVerify(fresh.DraftVerify), false,
			"only worth it against a small task_model; opt-in"},
		{"navigator (enabled)", navEnable, true,
			"keyword-first routing with model fallback"},
		{"navigator (auto)", navAuto, true,
			"pay for the navigator model only on ambiguous prompts"},
		{"thinking (nil = ON)", fresh.Thinking == nil || *fresh.Thinking, true,
			"models without chain-of-thought are worse; /think off is the opt-out"},
	}
	for _, tc := range bools {
		if tc.got != tc.want {
			t.Errorf("fresh default %s = %v, want %v (%s)", tc.knob, tc.got, tc.want, tc.why)
		}
	}

	// task_model empty = workers inherit the coordinator's provider.
	if _, ok := resolveTaskWorkerConfig(fresh, config.Config{Model: "m", BaseURL: "http://127.0.0.1:8080/v1"}); ok {
		t.Error("fresh default task_model should not override the worker backend")
	}

	// Host-gated autos stay nil (= auto): cache_prompt / slot_cache /
	// darwin_parallel / task_parallel decide per base URL, never for
	// cloud endpoints. Slot cache: attempted on local hosts only.
	if fresh.CachePrompt != nil || fresh.DarwinParallel != nil || fresh.TaskParallel != nil {
		t.Error("fresh parallel/cache knobs must be nil (host-dependent auto)")
	}
	if llm.NewSlotCache("http://127.0.0.1:8080/v1", fresh.SlotCache) == nil {
		t.Error("fresh slot_cache should auto-enable on a local host")
	}
	if llm.NewSlotCache("https://api.openai.com/v1", fresh.SlotCache) != nil {
		t.Error("fresh slot_cache must never probe a cloud endpoint")
	}

	// Zero-sentinel int knobs: 0 = built-in default resolved at the
	// point of use. Pinned so a stray "default" written into the
	// struct would surface here.
	if fresh.PruneProtectTokens != 0 { // loop resolves 0 → 8192
		t.Error("prune_protect_tokens zero value must stay 0 (loop default 8192)")
	}
	if fresh.MemoryBriefingTokens != 0 { // memory resolves 0 → 700/300 by tier
		t.Error("memory_briefing_tokens zero value must stay 0 (700/300 by tier)")
	}
	if fresh.DraftVerifyMaxRounds != 0 { // draft-verify resolves 0 → 2
		t.Error("draft_verify_max_rounds zero value must stay 0 (default 2)")
	}
	if fresh.TaskMaxSteps != 0 || fresh.TaskMaxTokens != 0 { // 0 = spec-or-10 / no cap
		t.Error("task step/token caps zero values must stay 0")
	}
	if fresh.ContextWindow != 0 { // 0 = provider metadata / learned / default
		t.Error("context_window zero value must stay 0 (auto-resolve)")
	}

	// Worker retention/concurrency are constants (no toml knobs — "just
	// works"), env-overridable via SUPERCLI_WORKER_RETENTION /
	// SUPERCLI_MAX_ACTIVE_WORKERS. Pinned so a change is a conscious
	// decision: retention 20 keeps enough finished workers for
	// send_message follow-ups without leaking whole conversations;
	// 6 concurrent active workers is plenty for one coordinator turn on
	// a local host, and over-limit spawns fail fast with guidance.
	if agent.DefaultFinishedWorkerRetention != 20 {
		t.Errorf("finished-worker retention = %d, want 20", agent.DefaultFinishedWorkerRetention)
	}
	if agent.DefaultMaxActiveWorkers != 6 {
		t.Errorf("max active workers = %d, want 6", agent.DefaultMaxActiveWorkers)
	}
}

func TestResolveOrchestratorModes(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name           string
		in             *bool
		want           orchestratorMode
		wantHard       bool
		wantDelegation bool
	}{
		{"unset is adaptive", nil, orchestratorAdaptive, false, true},
		{"true is always", &on, orchestratorAlways, true, true},
		{"false is never", &off, orchestratorNever, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOrchestratorMode(tc.in)
			if got != tc.want {
				t.Fatalf("mode = %v, want %v", got, tc.want)
			}
			if got.hard() != tc.wantHard || got.delegationEnabled() != tc.wantDelegation {
				t.Fatalf("mode flags hard/delegation = %v/%v, want %v/%v", got.hard(), got.delegationEnabled(), tc.wantHard, tc.wantDelegation)
			}
		})
	}
}
