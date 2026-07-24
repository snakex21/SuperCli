package app

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/account/credits"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	"supercli/internal/system/stats"
)

// statusBarDeps is the closed-over state needed to render the TUI footer.
// Built once at startup; the returned StatusFn is pull-style and safe to call
// from any render.
type statusBarDeps struct {
	goalSvc          *goal.Service
	loop             *agent.Loop
	tracker          *credits.Tracker
	draftStats       *stats.Memory
	caps             *llm.CapabilityRegistry
	home             string
	hasActiveProject bool
	projectName      string
	windowFor        func(model string) int
	agentTool        *agent.AgentTool
}

func buildStatusFn(d statusBarDeps) func() string {
	return func() string {
		goalLine := d.goalSvc.StatusLine(context.Background())
		activeProvider := d.loop.Provider()
		activeModel := activeProvider.Name()
		cred := credits.StatusLine(d.tracker, activeModel)
		// F34: live token counter and cost projection.
		tokens := ""
		costStr := ""
		if d.draftStats != nil {
			turns := d.draftStats.Snapshot()
			total := stats.Sum(turns)
			totalTokens := total.TokensIn + total.TokensOut
			if totalTokens > 0 {
				tokens = compactNum(totalTokens)
				// Calculate cost from current model rates, including per-provider
				// OpenRouter/proxy prices when the capability registry knows which
				// configured provider owns the active model.
				if !isSubscriptionRuntimeProvider(activeProvider) {
					r, _ := credits.RateForProvider(providerNameForModel(d.caps, activeModel), activeModel)
					inputCost := float64(total.TokensIn) / 1000.0 * r.InputPer1k
					outputCost := float64(total.TokensOut) / 1000.0 * r.OutputPer1k
					totalCost := inputCost + outputCost
					if totalCost > 0 {
						costStr = fmt.Sprintf("$%.4f", totalCost)
					}
				}
			}
		}
		var lines []string
		// Workspace header: active project · working directory · model.
		// Always shown so the user knows where the agent is rooted and
		// which model is answering, at a glance.
		var head []string
		if d.hasActiveProject {
			head = append(head, "proj: "+d.projectName)
		}
		if dir := shortenDir(d.home); dir != "" {
			head = append(head, "dir: "+dir)
		}
		if activeModel != "" {
			head = append(head, "model: "+activeModel)
		}
		// Orchestrator mode badge: the main loop is delegation-only, so
		// surface it next to the model like the coordinator conventions.
		if supercliOrchestratorMode {
			head = append(head, "orch")
		}
		if len(head) > 0 {
			lines = append(lines, strings.Join(head, " │ "))
		}
		if goalLine != "" {
			lines = append(lines, goalLine)
		}
		var bottom []string
		if cred != "" {
			bottom = append(bottom, cred)
		}
		// Reasoning effort badge, next to the model name, only
		// when set and applicable to the active model.
		if configured, effective, adjusted := llm.ReasoningEffortAdjustment(d.loop.Provider().Name()); effective != "" {
			if adjusted {
				bottom = append(bottom, "effort: "+configured+"→"+effective)
			} else {
				bottom = append(bottom, "effort: "+effective)
			}
		}
		// Thinking soft-switch state: only surfaced when the user has
		// turned it off AND the active model honours it (Qwen), so the
		// user knows /no_think is being appended.
		if !llm.ThinkingEnabled() && llm.SupportsThinkingSoftSwitch(d.loop.Provider().Name()) {
			bottom = append(bottom, "think: off")
		}
		if tokens != "" {
			tokStr := tokens
			if costStr != "" {
				tokStr += " │ " + costStr
			}
			bottom = append(bottom, "tok: "+tokStr)
		}
		// Context-window usage: last turn's prompt tokens as a share of
		// the active model's window. Only shown when the window size is
		// known (provider metadata / capability registry / learned /
		// config) — otherwise a percentage would be misleading.
		if prompt, ok := d.loop.LastTurnPromptTokens(); ok && prompt > 0 {
			if win := d.windowFor(activeModel); win > 0 {
				pct := prompt * 100 / win
				bottom = append(bottom, fmt.Sprintf("ctx:%d%%", pct))
			}
		}
		// Cache-miss telemetry for the last turn: the prompt split into
		// tokens the backend served from its KV/prompt cache vs tokens
		// it had to (re-)evaluate, plus the generated count. This is
		// the cache-miss hunting line — a healthy warm turn evaluates
		// roughly only the new tokens; a large eval means the prefix
		// churned and the client, not the hardware, is the bottleneck.
		// Backends that report no usage at all show nothing; backends
		// without a cached breakdown show cache 0 (pessimistic view).
		if cached, evaled, gen, ok := d.loop.LastTurnBreakdown(); ok && cached+evaled+gen > 0 {
			line := fmt.Sprintf("cache %s | eval %s | gen %s",
				compactNum(cached), compactNum(evaled), compactNum(gen))
			if _, reasoning, ok := d.loop.LastTurnStats(); ok && reasoning > 0 {
				line += fmt.Sprintf(" think:%d", reasoning)
			}
			bottom = append(bottom, line)
		}
		// Codex subscription usage (5h rolling + weekly), pulled from
		// the active provider's last /responses headers. Rendered only
		// when the active provider is Codex AND a snapshot has arrived.
		if rp, ok := llm.Unwrap(d.loop.Provider()).(interface {
			RateLimits() (llm.CodexRateLimits, bool)
		}); ok {
			if rl, ok := rp.RateLimits(); ok {
				if hud := rl.FormatHUD(); hud != "" {
					tile := "limit: " + hud
					// Multi-account: append which account is active
					// AND the pool-wide average, so the user sees both
					// "this account" and "all accounts combined".
					if rt, ok := llm.Unwrap(d.loop.Provider()).(*llm.RouterProvider); ok {
						snaps, _, active := rt.PoolUsage()
						if len(snaps) > 1 {
							tile += fmt.Sprintf(" · acct: %s (%d/%d)", rt.ActiveLabel(), active+1, len(snaps))
							if p5, p7, n := rt.PoolAggregate(); n > 0 {
								tile += fmt.Sprintf(" · pool %dacct 5h ~%d%% 7d ~%d%%", n, p5, p7)
							}
						}
					}
					bottom = append(bottom, tile)
				}
			}
		}
		// Fala 3: inline worker visibility. Show a compact tile
		// ("2 running · 1 done") whenever the coordinator has spawned
		// workers, so the user sees activity without typing /workers.
		if d.agentTool != nil && d.agentTool.Workers != nil {
			if tile := d.agentTool.Workers.Counts().StatusTile(); tile != "" {
				bottom = append(bottom, "workers: "+tile)
			}
		}
		if len(bottom) > 0 {
			lines = append(lines, strings.Join(bottom, " │ "))
		}
		return strings.Join(lines, "\n")
	}
}
