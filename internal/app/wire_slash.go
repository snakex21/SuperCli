package app

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/account/credits"
	"supercli/internal/agent"
	"supercli/internal/agent/reflect"
	"supercli/internal/llm"
	"supercli/internal/llm/consult"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/tools"
	"supercli/internal/tools/sandbox"
	"supercli/internal/ui/tui"
)

// slashWireDeps holds locals closed over by slash-command handlers.
// Main fills the fields available at each wire call site.
type slashWireDeps struct {
	home               string
	dataDir            string
	cwd                string
	sessionID          string
	uiLanguage         string
	tomlCfg            config.TomlConfig
	loop               *agent.Loop
	at                 *agent.AgentTool
	tracker            *credits.Tracker
	provider           llm.Provider
	caps               *llm.CapabilityRegistry
	sessStore          *session.Store
	windowFor          func(model string) int
	slotCache          *llm.SlotCache
	injector           *reflect.Injector
	registry           *tools.Registry
	memStore           *memory.Store
	globalMemStore     *memory.Store
	memoryBriefing     string
	askCh              chan tools.AskRequest
	provMgr            *providers.Manager
	council            *consult.Council
	buildCouncilMember func(string) (llm.Provider, error)
}

// wireSlashEarly registers slash handlers that do not need the provider
// manager or council. MCP is registered separately in Main after initMcp.
func wireSlashEarly(cmds map[string]tui.SlashHandler, d slashWireDeps) {
	// Fala 3: /workers — coordinator visibility. Lists workers from the
	// task registry; "/workers stop <id>" cancels a running one.
	cmds["workers"] = func(ctx context.Context, args string) (string, error) {
		fields := strings.Fields(args)
		if len(fields) >= 1 && strings.EqualFold(fields[0], "stop") {
			if len(fields) < 2 {
				return "usage: /workers stop <id>   (id like worker-1; see /workers)", nil
			}
			id := fields[1]
			if !strings.HasPrefix(id, "worker-") {
				id = "worker-" + id
			}
			if err := d.at.Workers.Stop(id); err != nil {
				return fmt.Sprintf("workers: %v", err), nil
			}
			return fmt.Sprintf("workers: stop requested for %s", id), nil
		}
		return formatWorkers(d.at.Workers), nil
	}

	// Fala 3: /context — where the input tokens go (system prompt,
	// tool schemas, tool results, messages) plus the top 5 heaviest
	// items, so the user can see what is bloating the context.
	cmds["context"] = func(ctx context.Context, args string) (string, error) {
		return agent.FormatContextReport(d.loop.ContextReport()), nil
	}

	// /allow-all — toggle full filesystem access. Persists to config.toml.
	cmds["allow-all"] = func(ctx context.Context, args string) (string, error) {
		switch strings.ToLower(strings.TrimSpace(args)) {
		case "on", "true", "1":
			sandbox.SetUnsandboxed(true)
			workingDirNote = "Working directory: " + d.home +
				"\nFull filesystem access is ON (--allow-all). You can read and write files anywhere on the filesystem. Prefer absolute paths."
		case "off", "false", "0", "":
			sandbox.SetUnsandboxed(false)
			workingDirNote = "Working directory (file sandbox root): " + d.home +
				"\nUse this exact path for file and directory operations. Relative paths resolve here; paths must stay inside it."
		default:
			return "usage: /allow-all on|off", nil
		}
		globalPath, _ := config.FindTomlPaths(d.dataDir, d.cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			tc.AllowAll = sandbox.IsUnsandboxed()
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("allow-all: save config.toml: %v", err)
			}
		}
		if sandbox.IsUnsandboxed() {
			d.loop.InjectUserMessage(ctx, "[filesystem access] Full filesystem access is now ON. Absolute file and search paths outside the workspace are allowed; sensitive system folders remain blocked.")
			return "Full filesystem access is now ON — file operations can reach any directory (sensitive system paths still blocked). Persisted to config.toml.", nil
		}
		d.loop.InjectUserMessage(ctx, "[filesystem access] Workspace sandbox is now ON. File and search paths must stay inside the active workspace.")
		return "Sandbox is now ON — file operations restricted to the working directory. Persisted to config.toml.", nil
	}

	cmds["clear"] = func(ctx context.Context, args string) (string, error) {
		hidden := d.loop.HideLastUserTurns(2)
		if hidden == 0 {
			return "nothing to clear", nil
		}
		return fmt.Sprintf("cleared: hid %d message(s) from context (scrollback kept)", hidden), nil
	}

	// Wave 4: /resume — list recent sessions or load one back
	// into the live loop. The continuation is recorded under
	// the NEW session id (sessWriter keeps writing here); the
	// original session stays intact and searchable.
	cmds["resume"] = func(ctx context.Context, args string) (string, error) {
		if d.sessStore == nil {
			return "resume: session store unavailable", nil
		}
		args = strings.TrimSpace(args)
		if args == "" || strings.EqualFold(args, "all") {
			return listResumableSessions(ctx, d.sessStore, d.sessionID, d.home, strings.EqualFold(args, "all"))
		}
		out, err := resumeSession(ctx, d.loop, d.sessStore, d.windowFor, args)
		if err != nil {
			return fmt.Sprintf("resume: %v", err), nil
		}
		// Warm the server-side KV BEFORE the first request of the
		// resumed conversation. Restore is always safe: llama.cpp
		// re-evals from the first divergent token, so a stale or
		// mismatched slot file only costs the benefit.
		if n, rerr := d.slotCache.Restore(ctx, args); rerr != nil {
			log.Printf("slotcache: restore %s: %v (disabled for this session)", args, rerr)
		} else if n > 0 {
			log.Printf("slotcache: restored %d cached token(s) for %s", n, args)
		}
		return out, nil
	}

	// F25a: /help — list all registered slash commands.
	cmds["help"] = func(ctx context.Context, args string) (string, error) {
		// Short grouped list by default; /help all shows everything.
		if strings.TrimSpace(strings.ToLower(args)) == "all" {
			return tui.HelpContentAllFor(d.uiLanguage), nil
		}
		return tui.HelpContentFor(d.uiLanguage), nil
	}

	// F25a: /reflect — show learned patterns from reflection.
	cmds["reflect"] = func(ctx context.Context, args string) (string, error) {
		if d.injector == nil {
			return "reflect: no patterns learned yet (F5 memory store not available)", nil
		}
		suffix, err := d.injector.Build(ctx, "")
		if err != nil {
			return fmt.Sprintf("reflect: %v", err), nil
		}
		if suffix == "" {
			return "reflect: no relevant patterns found", nil
		}
		return suffix, nil
	}

	// F25a: /compact — real context compaction. The active
	// model summarizes the conversation (9-section prompt),
	// then every non-system message is replaced by a single
	// system message containing the summary plus a resume
	// wrapper. The dropped messages stay in the F13 session
	// store and remain searchable via search_history.
	cmds["compact"] = func(ctx context.Context, args string) (string, error) {
		msgs := d.loop.AllMessages()
		nonSystem := 0
		for _, m := range msgs {
			if m.Role != llm.RoleSystem {
				nonSystem++
			}
		}
		if nonSystem == 0 {
			return "compact: nothing to compact (already minimal)", nil
		}
		summary, err := summarizeForCompaction(ctx, d.loop.Provider(), msgs)
		if err != nil {
			return fmt.Sprintf("compact: summarization failed: %v (context unchanged)", err), nil
		}
		summary += compactFacts(msgs, d.registry.ActiveNames())
		removed := d.loop.CompactWithSummary(wrapCompactSummary(summary))
		return fmt.Sprintf("compact: replaced %d message(s) with a %d-char summary", removed, len(summary)), nil
	}

	// F25a: /status — show credits and session info.
	cmds["status"] = func(ctx context.Context, args string) (string, error) {
		sessUsed, dayUsed := d.tracker.Used()
		budget := d.tracker.Budget()
		name := d.provider.Name()
		var b strings.Builder
		fmt.Fprintf(&b, "model: %s\n", name)
		if budget.PerSession > 0 {
			fmt.Fprintf(&b, "session: %d / %d tokens (%.0f%%)\n",
				sessUsed, budget.PerSession, float64(sessUsed)/float64(budget.PerSession)*100)
		} else {
			fmt.Fprintf(&b, "session: %d tokens (no cap)\n", sessUsed)
		}
		if budget.PerDay > 0 {
			fmt.Fprintf(&b, "daily: %d / %d tokens (%.0f%%)\n",
				dayUsed, budget.PerDay, float64(dayUsed)/float64(budget.PerDay)*100)
		} else {
			fmt.Fprintf(&b, "daily: %d tokens (no cap)\n", dayUsed)
		}
		// Session-write health: silent-loss protection. One line
		// when everything is fine; the sticky first error, the
		// failure counter and the retry-buffer depth when not.
		ps := d.loop.PersistStatus()
		switch {
		case ps.Failures == 0:
			fmt.Fprintf(&b, "persistence: ok\n")
		default:
			state := "recovered (last write ok)"
			if !ps.LastWriteOK {
				state = "FAILING (last write failed)"
			}
			fmt.Fprintf(&b, "persistence: %s\n", state)
			fmt.Fprintf(&b, "  failures: %d (first: %s at %s — %s)\n",
				ps.Failures, ps.FirstOp, ps.FirstAt.Format("15:04:05"), ps.FirstErr)
			fmt.Fprintf(&b, "  last: %s at %s — %s\n",
				ps.LastOp, ps.LastAt.Format("15:04:05"), ps.LastErr)
			if ps.Pending > 0 {
				fmt.Fprintf(&b, "  buffered for retry: %d message(s)\n", ps.Pending)
			}
			if ps.ProjectionDirty {
				fmt.Fprintf(&b, "  context projection: dirty (retry pending)\n")
			}
			if ps.Dropped > 0 {
				fmt.Fprintf(&b, "  LOST to buffer overflow: %d message(s)\n", ps.Dropped)
			}
		}
		return b.String(), nil
	}

	// /reasoning — show or set the reasoning-effort level for
	// OpenAI-family reasoning models. Persisted to the global
	// config.toml; sent only to models that support the parameter.
	cmds["reasoning"] = func(ctx context.Context, args string) (string, error) {
		args = strings.ToLower(strings.TrimSpace(args))
		modelName := d.loop.Provider().Name()
		if args == "" {
			cur, effective, adjusted := llm.ReasoningEffortAdjustment(modelName)
			if cur == "" {
				cur = "(not set — provider default)"
			}
			note := ""
			if !llm.SupportsReasoningEffort(modelName) {
				note = fmt.Sprintf("\nnote: current model %q does not support reasoning effort; the parameter is not sent", modelName)
			}
			if supported, ok := llm.SupportedReasoningEfforts(modelName); ok {
				note += fmt.Sprintf("\nbackend-supported for %s: %s", modelName, strings.Join(supported, "|"))
			}
			if adjusted {
				note += fmt.Sprintf("\neffective for current model: %s (configured %s was adjusted from backend evidence)", effective, cur)
			}
			return fmt.Sprintf("reasoning effort: %s\nusage: /reasoning <%s|off>%s",
				cur, strings.Join(llm.ReasoningEffortLevels, "|"), note), nil
		}
		if args == "off" || args == "default" {
			args = ""
		}
		if err := llm.SetReasoningEffort(args); err != nil {
			return fmt.Sprintf("reasoning: %v", err), nil
		}
		// Persist to the GLOBAL config.toml (same file the
		// onboarding wizard and provider manager write).
		globalPath, _ := config.FindTomlPaths(d.dataDir, d.cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			tc.ReasoningEffort = args
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("reasoning: save config.toml: %v", err)
			}
		}
		if args == "" {
			return "reasoning effort cleared (provider default)", nil
		}
		out := fmt.Sprintf("reasoning effort set to %s", args)
		if !llm.SupportsReasoningEffort(modelName) {
			out += fmt.Sprintf("\nnote: current model %q does not support it; the parameter will apply when you switch to an OpenAI reasoning model", modelName)
		} else if configured, effective, adjusted := llm.ReasoningEffortAdjustment(modelName); adjusted {
			out += fmt.Sprintf("\nnote: current backend evidence adjusts %s -> %s for %s", configured, effective, modelName)
		}
		return out, nil
	}

	// /think — toggle chain-of-thought for local models that honour a
	// prompt soft switch (Qwen /no_think). Orthogonal to /reasoning
	// (cloud reasoning_effort). Default is on; `/think off` appends
	// /no_think to the trailing prompt to cut latency on Qwen.
	cmds["think"] = func(ctx context.Context, args string) (string, error) {
		args = strings.ToLower(strings.TrimSpace(args))
		modelName := d.loop.Provider().Name()
		if args == "" {
			state := "on"
			if !llm.ThinkingEnabled() {
				state = "off"
			}
			note := ""
			if !llm.SupportsThinkingSoftSwitch(modelName) {
				note = fmt.Sprintf("\nnote: current model %q has no prompt thinking switch; /think off has no effect on it (use /reasoning for cloud reasoning models)", modelName)
			}
			return fmt.Sprintf("thinking: %s\nusage: /think <on|off>%s", state, note), nil
		}
		var on bool
		switch args {
		case "on", "true", "1":
			on = true
		case "off", "false", "0":
			on = false
		default:
			return "usage: /think <on|off>", nil
		}
		llm.SetThinkingEnabled(on)
		// Persist to the GLOBAL config.toml.
		globalPath, _ := config.FindTomlPaths(d.dataDir, d.cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			v := on
			tc.Thinking = &v
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("think: save config.toml: %v", err)
			}
		}
		state := "on"
		if !on {
			state = "off"
		}
		out := fmt.Sprintf("thinking set to %s", state)
		if !on && !llm.SupportsThinkingSoftSwitch(modelName) {
			out += fmt.Sprintf("\nnote: current model %q has no prompt thinking switch; the setting applies when you switch to a Qwen-family model", modelName)
		}
		return out, nil
	}

	// /orchestrator — three delegation modes. Unlike /think it does NOT
	// apply mid-session: it swaps the main loop's tool list, and changing
	// the tool list in flight would break the KV-cache prefix (chat
	// templates serialize `tools` at the very start of the prompt), so it
	// persists to config.toml and takes effect on the next launch.
	cmds["orchestrator"] = func(ctx context.Context, args string) (string, error) {
		args = strings.ToLower(strings.TrimSpace(args))
		curState := "auto"
		if supercliOrchestratorMode {
			curState = "on"
		} else if supercliDelegationDisabled {
			curState = "off"
		}
		if args == "" {
			return fmt.Sprintf("orchestrator: %s (this session)\nusage: /orchestrator <auto|on|off>   — auto=delegate when useful, on=always, off=never; next launch", curState), nil
		}
		var saved *bool
		want := "auto"
		switch args {
		case "auto", "default":
			saved = nil
		case "on", "true", "1":
			v := true
			saved = &v
			want = "on"
		case "off", "false", "0":
			v := false
			saved = &v
			want = "off"
		default:
			return "usage: /orchestrator <auto|on|off>", nil
		}
		// Persist to the GLOBAL config.toml.
		globalPath, _ := config.FindTomlPaths(d.dataDir, d.cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			tc.Orchestrator = saved
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("orchestrator: save config.toml: %v", err)
				return "orchestrator: failed to save config", nil
			}
		}
		if want == curState {
			return fmt.Sprintf("orchestrator is already %s and saved.", want), nil
		}
		return fmt.Sprintf("orchestrator set to %s — takes effect on the next launch (new session). This session keeps its current tool set.", want), nil
	}

	// /orchestrator-model — which model plays the COORDINATOR (the one
	// that writes task briefs). Counterpart of task_model (the worker).
	// Format: "model" or "provider/model". Empty restores the default
	// (the main model coordinates). Persists to config.toml; takes
	// effect on the next launch, same KV-cache reason as /orchestrator.
	cmds["orchestrator-model"] = func(ctx context.Context, args string) (string, error) {
		args = strings.TrimSpace(args)
		if args == "" || args == "default" || args == "off" || args == "none" {
			globalPath, _ := config.FindTomlPaths(d.dataDir, d.cwd)
			if tc, err := config.LoadToml(globalPath); err == nil {
				tc.OrchestratorModel = ""
				if err := config.SaveToml(globalPath, tc); err != nil {
					log.Printf("orchestrator-model: save config.toml: %v", err)
					return "orchestrator-model: failed to save config", nil
				}
			}
			return "orchestrator-model cleared — the main model coordinates from the next launch.", nil
		}
		globalPath, _ := config.FindTomlPaths(d.dataDir, d.cwd)
		if tc, err := config.LoadToml(globalPath); err == nil {
			tc.OrchestratorModel = args
			if err := config.SaveToml(globalPath, tc); err != nil {
				log.Printf("orchestrator-model: save config.toml: %v", err)
				return "orchestrator-model: failed to save config", nil
			}
		}
		return fmt.Sprintf("orchestrator-model set to %q — the coordinator uses it from the next launch (new session). Pair with task_model for a two-model setup.", args), nil
	}

	// /usage — force a fresh fetch of the ChatGPT-subscription usage
	// limits (5h rolling + weekly window) from the dedicated usage
	// endpoint and print them. This is NOT a completion: it hits the
	// usage endpoint directly and does not consume the quota. When the
	// active provider is not Codex (or has no auth) it prints a clear
	// message instead of an error.
	cmds["usage"] = func(ctx context.Context, args string) (string, error) {
		prov := llm.Unwrap(d.loop.Provider())
		_, single := prov.(codexUsageFetcher)
		_, all := prov.(codexUsageAllFetcher)
		if !single && !all {
			return "the active model is not a ChatGPT-subscription (Codex) model — usage limits are only available there.\nRun /login and /model gpt-5.5 to switch.", nil
		}

		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		// Refresh BEFORE building the pool table so every account's
		// snapshot is current. For a multi-account router this fetches
		// ALL accounts (each with its own token), so the pool table and
		// POOL aggregate reflect every account — not just the active one.
		rl, err := refreshCodexUsage(fctx, prov)
		// The per-account pool table (multi-account router) is built
		// from each account's last-known snapshot AFTER the refresh, so
		// it shows the freshly-fetched numbers; it stays useful even
		// when some accounts failed to refresh (expired token, offline).
		pool := codexPoolUsageDetail(prov)
		// Partial success (multi-account): the active account refreshed
		// fine but another account's token failed. Don't treat that as a
		// total failure — show the fresh active numbers and pool table,
		// noting which account(s) could not refresh.
		if err != nil && rl.OK {
			return fmt.Sprintf("Codex usage (just refreshed; some accounts failed: %v):\n%s%s",
				err, rl.FormatDetail(), pool), nil
		}
		if err != nil {
			if rp, ok := prov.(interface {
				RateLimits() (llm.CodexRateLimits, bool)
			}); ok {
				if cached, ok := rp.RateLimits(); ok {
					return "could not refresh (showing last known):\n" + cached.FormatDetail() + pool, nil
				}
			}
			// No snapshot for the active account, but the pool may
			// still have per-account data worth showing. Either way the
			// real reason (HTTP status, body, URL) MUST be surfaced —
			// dropping err here is what made /usage print a bare
			// "could not refresh the active account:" with nothing after.
			if pool != "" {
				return fmt.Sprintf("could not refresh the active account: %v%s", err, pool), nil
			}
			return fmt.Sprintf("could not fetch Codex usage: %v", err), nil
		}
		return "Codex usage (just refreshed):\n" + rl.FormatDetail() + pool, nil
	}

	// Wave 2 B6: /memory — inspect persistent memory. No args:
	// overview (recent entries, DB sizes, embedding status).
	// `/memory search <q>` runs a hybrid search over both stores;
	// `/memory forget <id>` deletes an entry wherever it lives.
	cmds["memory"] = func(ctx context.Context, args string) (string, error) {
		return memoryCommand(ctx, d.memStore, d.globalMemStore, d.memoryBriefing, args)
	}

	// /projects — manage the per-project memory map. Backed by
	// internal/storage/memory/projects.go; the slash command is a
	// thin shell that calls into app.projectsCommand. The
	// interactive TUI menu (opened from the 'p' shortcut or any
	// /projects invocation without args) lives in tui/menu_projects.go.
	cmds["projects"] = func(ctx context.Context, args string) (string, error) {
		return projectsCommand(ctx, args, d.dataDir)
	}
}

// wireSlashLate registers handlers that need the provider manager and
// council (after provMgr + buildCouncilMember + consult tool setup).
func wireSlashLate(cmds map[string]tui.SlashHandler, d slashWireDeps) {
	// /council — manual brainstorming across hand-picked models.
	//
	//	/council               → multi-select roster picker
	//	                         (space toggles, enter confirms;
	//	                          selection persists in config.toml
	//	                          under [council] models)
	//	/council <question>    → ask the saved roster in parallel
	//	/council N <question>  → legacy: auto cheapest-N pool
	//	                         (used only when no roster is saved)
	cmds["council"] = func(ctx context.Context, args string) (string, error) {
		q := strings.TrimSpace(args)
		if q == "" {
			// Interactive roster picker via the ask_user UI.
			opts := councilPickerOptions(d.provMgr, d.caps)
			if len(opts) == 0 {
				return "council: no models available — add providers via /providers first", nil
			}
			respond := make(chan tools.AskAnswer, 1)
			req := tools.AskRequest{
				ID:          "council-pick",
				Question:    "Pick council members (space toggles, enter confirms)",
				Header:      "council",
				Options:     opts,
				MultiSelect: true,
				Respond:     respond,
			}
			select {
			case d.askCh <- req:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			select {
			case ans := <-respond:
				if ans.Cancelled || len(ans.Selected) == 0 {
					return "council: selection cancelled", nil
				}
				if err := d.provMgr.SaveCouncilModels(ans.Selected); err != nil {
					log.Printf("council: save roster failed: %v", err)
				}
				return fmt.Sprintf("council roster saved: %s\nask away: /council <question>",
					strings.Join(ans.Selected, ", ")), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		// Roster: last picker selection (global config.toml) or
		// the merged [council] models section (project override).
		roster := d.provMgr.LoadCouncilModels()
		if len(roster) == 0 {
			roster = d.tomlCfg.Council.Models
		}
		// Legacy optional leading N (only meaningful for the
		// auto-pool fallback path).
		n := 3
		if fields := strings.Fields(q); len(fields) > 1 {
			if v, err := strconv.Atoi(fields[0]); err == nil && v > 0 {
				n = v
				q = strings.TrimSpace(strings.TrimPrefix(q, fields[0]))
			}
		}
		if len(roster) == 0 {
			// Fallback: auto cheapest-N council with judge pick.
			if len(d.council.Samples) == 0 {
				return "council: no roster picked yet — run /council (no args) to choose models", nil
			}
			res, err := d.council.Consult(ctx, consult.Request{Question: q, N: n})
			if err != nil {
				return fmt.Sprintf("council: %v", err), nil
			}
			if res.AllFailed {
				return "council: all samples failed", nil
			}
			w := res.Candidates[res.Verdict.WinnerIndex]
			return fmt.Sprintf("winner (#%d, %s):\n%s\n\njudge: %s\n[%d candidate(s), %d tokens]",
				res.Verdict.WinnerIndex+1, w.Provider, w.Response, res.Verdict.Reason,
				len(res.Candidates), res.TotalTokens), nil
		}
		// Hand-picked roster: build each member; a single bad
		// model never aborts the rest.
		var provs []llm.Provider
		var specs []string
		var buildErrs []string
		for _, s := range roster {
			p, err := d.buildCouncilMember(s)
			if err != nil {
				buildErrs = append(buildErrs, fmt.Sprintf("model %s: error: %v", s, err))
				continue
			}
			provs = append(provs, p)
			specs = append(specs, s)
		}
		if len(provs) == 0 {
			return "council: no usable models in roster: " + strings.Join(buildErrs, "; "), nil
		}
		cc := &consult.Council{Samples: provs, Judge: d.loop.Provider()}
		res, err := cc.ConsultSelected(ctx, q, provs)
		if err != nil {
			return fmt.Sprintf("council: %v", err), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "council × %d — %s\n", len(provs), q)
		for i, cd := range res.Candidates {
			if cd.Err != nil {
				fmt.Fprintf(&b, "\n━━ %s · error ━━\nmodel %s: error: %v\n", specs[i], specs[i], cd.Err)
				continue
			}
			fmt.Fprintf(&b, "\n━━ %s · %s · in %d / out %d tok ━━\n%s\n",
				specs[i], cd.Elapsed.Round(time.Millisecond), cd.In, cd.Out,
				strings.TrimSpace(cd.Response))
		}
		for _, e := range buildErrs {
			b.WriteString("\n" + e + "\n")
		}
		if w := res.Verdict.WinnerIndex; w >= 0 && w < len(specs) {
			fmt.Fprintf(&b, "\njudge (%s): winner=%s · %s\n", d.loop.Provider().Name(), specs[w], res.Verdict.Reason)
		} else if res.Verdict.Reason != "" {
			fmt.Fprintf(&b, "\njudge: %s\n", res.Verdict.Reason)
		}
		fmt.Fprintf(&b, "[%d model(s), %d total tokens]", len(res.Candidates), res.TotalTokens)
		return b.String(), nil
	}

	// ChatGPT-subscription auth: /login runs the OAuth+PKCE
	// browser flow and registers a "codex" provider entry;
	// /logout clears the saved tokens.
	cmds["login"] = func(ctx context.Context, args string) (string, error) {
		if codexAuthMgr == nil {
			initCodexAuth(d.dataDir, d.tomlCfg)
		}
		// Multi-account: "/login <label>" signs a SECOND (named)
		// account into auth-<label>.json. Bare "/login" uses the
		// default account exactly as before.
		label := strings.TrimSpace(args)
		mgr := codexAuthMgr
		if label != "" {
			mgr = codexauth.NewManagerFor(d.dataDir, label, codexauth.Options{
				ClientID:   d.tomlCfg.CodexAuth.ClientID,
				Issuer:     d.tomlCfg.CodexAuth.Issuer,
				BackendURL: d.tomlCfg.CodexAuth.BackendURL,
			})
		}
		var status strings.Builder
		res, err := mgr.Login(ctx, &status)
		if err != nil {
			out := strings.TrimSpace(status.String())
			if out != "" {
				return out + "\n" + fmt.Sprintf("login failed: %v", err), nil
			}
			return fmt.Sprintf("login failed: %v", err), nil
		}
		// Register a "codex" provider entry so /model and the
		// provider menus can route through the ChatGPT backend.
		if d.provMgr != nil {
			if err := d.provMgr.Add("codex", config.ProviderCodex,
				mgr.Options().BackendURL, "", "gpt-5.5"); err != nil &&
				!strings.Contains(err.Error(), "already exists") {
				log.Printf("login: register codex provider: %v", err)
			}
			d.provMgr.Reload()
		}
		// Register the Codex model family in the capability
		// registry so /model gpt-5.5 resolves immediately
		// (the ChatGPT backend has no /v1/models to probe).
		llm.RegisterCodexCatalog(d.caps, "codex")
		plan := res.PlanType
		if plan == "" {
			plan = "unknown plan"
		}
		return fmt.Sprintf("logged in with ChatGPT (%s).\nUse /model to switch to a Codex model (e.g. gpt-5.5) — requests now route through the ChatGPT backend.", plan), nil
	}
	cmds["logout"] = func(ctx context.Context, args string) (string, error) {
		// Multi-account: "/logout <label>" removes that named
		// account's auth-<label>.json. Bare "/logout" removes the
		// default account (and its usage snapshot) as before.
		label := strings.TrimSpace(args)
		if label != "" {
			mgr := codexauth.NewManagerFor(d.dataDir, label, codexauth.Options{})
			if !mgr.LoggedIn() {
				return fmt.Sprintf("account %s is not logged in", strconv.Quote(label)), nil
			}
			if err := mgr.Logout(); err != nil {
				return "", fmt.Errorf("logout %s: %w", label, err)
			}
			return fmt.Sprintf("logged out account %s (credentials removed)", strconv.Quote(label)), nil
		}
		if codexAuthMgr == nil || !codexAuthMgr.LoggedIn() {
			return "not logged in (no ChatGPT credentials saved)", nil
		}
		if err := codexAuthMgr.Logout(); err != nil {
			return "", fmt.Errorf("logout: %w", err)
		}
		// Drop the saved usage snapshot too, so the HUD does not keep
		// showing the logged-out account's rate limits.
		if err := llm.ClearCodexRateLimits(d.dataDir); err != nil {
			log.Printf("logout: clear usage snapshot: %v", err)
		}
		return "logged out — ChatGPT credentials and saved usage limits removed from the data dir", nil
	}

	// /accounts lists all logged-in ChatGPT accounts (default +
	// any named ones). With 2+, requests round-robin across them.
	cmds["accounts"] = func(ctx context.Context, args string) (string, error) {
		labels, err := codexauth.ListAccounts(d.dataDir)
		if err != nil {
			return "", fmt.Errorf("accounts: %w", err)
		}
		var loggedIn []string
		for _, label := range labels {
			mgr := codexauth.NewManagerFor(d.dataDir, label, codexauth.Options{})
			if mgr.LoggedIn() {
				loggedIn = append(loggedIn, label)
			}
		}
		if len(loggedIn) == 0 {
			return "no ChatGPT accounts logged in. Use /login (or /login <label>) to add one.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "ChatGPT accounts (%d):\n", len(loggedIn))
		for _, label := range loggedIn {
			fmt.Fprintf(&b, "  - %s\n", label)
		}
		if len(loggedIn) > 1 {
			b.WriteString("requests round-robin across these accounts.")
		} else {
			b.WriteString("add another with /login <label> to enable round-robin.")
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}

	// /account — show the current ChatGPT-subscription login (account
	// id + plan) without hitting the network. Read-only counterpart to
	// /login and /logout.
	cmds["account"] = func(ctx context.Context, args string) (string, error) {
		if codexAuthMgr == nil {
			initCodexAuth(d.dataDir, d.tomlCfg)
		}
		info, err := codexAuthMgr.Account()
		if err != nil {
			return "", fmt.Errorf("account: %w", err)
		}
		if !info.LoggedIn {
			return "not logged in — run /login to sign in with ChatGPT.", nil
		}
		plan := info.PlanType
		if plan == "" {
			plan = "unknown"
		}
		acct := info.AccountID
		if acct == "" {
			acct = "(unknown)"
		}
		var b strings.Builder
		b.WriteString("ChatGPT account\n")
		b.WriteString(fmt.Sprintf("  plan:    %s\n", plan))
		b.WriteString(fmt.Sprintf("  account: %s", acct))
		if !info.LastRefresh.IsZero() {
			b.WriteString(fmt.Sprintf("\n  refreshed: %s", info.LastRefresh.Format("2006-01-02 15:04")))
		}
		return b.String(), nil
	}

	// F25a: /sandbox — show sandbox status.
	cmds["sandbox"] = func(ctx context.Context, args string) (string, error) {
		status := "restricted"
		allowHint := ""
		if sandbox.IsUnsandboxed() {
			status = "allow-all (full filesystem access)"
			allowHint = "\nuse /allow-all off to re-enable the sandbox"
		} else {
			allowHint = "\nuse /allow-all on for full filesystem access"
		}
		return fmt.Sprintf("sandbox: %s\nhome: %s\ndata: %s%s", status, d.home, d.dataDir, allowHint), nil
	}
}
