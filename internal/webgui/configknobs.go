package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// This file is the web counterpart of the TUI /settings panel
// (internal/ui/tui/settings.go): the same ordered knob list over the
// same global config.toml, with the same tri-state / int / text
// semantics and the same live side effects (thinking, cache_prompt).
// Kept in sync by hand; both surfaces read the file fresh on every
// request so neither can clobber the other's writes.

// knobKind classifies how a knob is edited, mirroring settingKind in
// the TUI. The wire values are strings so the front-end can switch on
// them without magic numbers.
const (
	knobTri      = "tri"      // default → on → off ("default" resets)
	knobTriAuto  = "tri_auto" // like tri but nil means host-dependent auto
	knobNav      = "nav"      // navigator: auto/on/off
	knobInt      = "int"      // integer, 0/empty = built-in default
	knobText     = "text"     // free text, empty = built-in default
	knobReadonly = "readonly" // display only
)

// knobView is one row of the settings panel as sent to the browser.
type knobView struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Desc        string `json:"desc"`
	Kind        string `json:"kind"`
	Value       string `json:"value"`           // display value
	Source      string `json:"source"`          // "default" | "manual"
	Raw         string `json:"raw"`             // editable raw value (int/text kinds)
	State       string `json:"state,omitempty"` // tri/nav kinds: "default" | "on" | "off"
	NextSession bool   `json:"next_session"`
}

// knobDef is the static part of a knob row.
type knobDef struct {
	key         string
	desc        string
	kind        string
	nextSession bool
}

// knobDefs mirrors settingsRows() in the TUI — same keys, same order.
func knobDefs() []knobDef {
	return []knobDef{
		{"orchestrator", "on: keep the main chat lean by delegating work; off: remove worker tools and work directly", knobTri, false},
		{"thinking", "chain-of-thought for local soft-switch models (Qwen /no_think)", knobTri, false},
		{"navigator", "pre-request route (chat/advisor/coordinator) decision mode", knobNav, true},
		{"stable_toolset", "keep the tools list fixed all session (KV-cache friendly)", knobTri, true},
		{"cache_prompt", "ask local llama.cpp servers to reuse the KV prompt cache", knobTriAuto, true},
		{"darwin_parallel", "run darwin best-of-N agents concurrently vs one at a time", knobTriAuto, true},
		{"task_parallel", "run multiple task delegations from one turn concurrently", knobTriAuto, true},
		{"memory_briefing_tokens", "hard token budget for the session-start memory briefing", knobInt, true},
		{"task_max_steps", "cap on model turns a delegated worker may take", knobInt, true},
		{"task_max_tokens", "cap on a delegated worker's total token spend", knobInt, true},
		{"task_model", "worker model/host for task delegation (\"model\" or \"provider/model\"; empty = coordinator's model)", knobText, true},
		{"noop_gate", "skip a batch (-p) run with zero LLM calls when nothing changed since the last identical run", knobTri, false},
		{"preflight_repo", "append a compact repo-state block to the first message and worker briefings (git optional)", knobTri, true},
		{"draft_verify", "worker drafts file changes; objective sieve + big-model verdict gate them", knobTri, true},
		{"draft_verify_max_rounds", "cap on REVISE round-trips before the big model takes over", knobInt, true},
		{"verify_commands", "objective sieve for draft-verify — semicolon-separated (e.g. go build ./... ; go test ./...)", knobText, true},
		{"default_model", "startup model — change from the model picker", knobReadonly, false},
		{"default_provider", "startup provider — change from the model picker or providers panel", knobReadonly, false},
	}
}

// knobValue computes a knob's display value, source and editable raw
// form from the loaded config, mirroring settingValueSource in the TUI.
func knobValue(c *config.TomlConfig, key string) (value, source, raw string) {
	switch key {
	case "orchestrator":
		v, s := triKnob(c.Orchestrator, "off")
		return v, s, ""
	case "thinking":
		v := "on"
		if !llm.ThinkingEnabled() {
			v = "off"
		}
		if c.Thinking == nil {
			return v, "default", ""
		}
		return v, "manual", ""
	case "navigator":
		if strings.TrimSpace(c.Navigator) == "" {
			return "auto", "default", ""
		}
		return c.Navigator, "manual", ""
	case "stable_toolset":
		v, s := triKnob(c.StableToolset, "on")
		return v, s, ""
	case "cache_prompt":
		v, s := triAutoKnob(c.CachePrompt, "on", "off")
		return v, s, ""
	case "darwin_parallel":
		v, s := triAutoKnob(c.DarwinParallel, "parallel", "sequential")
		return v, s, ""
	case "task_parallel":
		v, s := triAutoKnob(c.TaskParallel, "parallel", "sequential")
		return v, s, ""
	case "memory_briefing_tokens":
		return intKnob(c.MemoryBriefingTokens, "700/300 by tier")
	case "task_max_steps":
		return intKnob(c.TaskMaxSteps, "spec or 10")
	case "task_max_tokens":
		return intKnob(int(c.TaskMaxTokens), "no cap")
	case "task_model":
		if strings.TrimSpace(c.TaskModel) == "" {
			return "default (coordinator's model)", "default", ""
		}
		return c.TaskModel, "manual", c.TaskModel
	case "noop_gate":
		v, s := triKnob(c.NoopGate, "off")
		return v, s, ""
	case "preflight_repo":
		v, s := triKnob(c.PreflightRepo, "on")
		return v, s, ""
	case "draft_verify":
		v, s := triKnob(c.DraftVerify, "off")
		return v, s, ""
	case "draft_verify_max_rounds":
		return intKnob(c.DraftVerifyMaxRounds, "2")
	case "verify_commands":
		if len(c.VerifyCommands) == 0 {
			return "none (diff-only verdict)", "default", ""
		}
		joined := strings.Join(c.VerifyCommands, " ; ")
		return joined, "manual", joined
	case "default_model":
		return dashKnob(c.DefaultModel), "set via model picker", ""
	case "default_provider":
		return dashKnob(c.DefaultProvider), "set via providers", ""
	}
	return "", "", ""
}

func triKnob(p *bool, def string) (value, source string) {
	if p == nil {
		return def, "default"
	}
	if *p {
		return "on", "manual"
	}
	return "off", "manual"
}

func triAutoKnob(p *bool, on, off string) (value, source string) {
	if p == nil {
		return "auto", "default"
	}
	if *p {
		return on, "manual"
	}
	return off, "manual"
}

func intKnob(v int, def string) (value, source, raw string) {
	if v == 0 {
		return "default (" + def + ")", "default", ""
	}
	return strconv.Itoa(v), "manual", strconv.Itoa(v)
}

// knobState returns the raw switch position for tri/nav kinds so the
// front-end's segmented control does not have to reverse-map display
// labels like "parallel"/"sequential" back onto on/off.
func knobState(c *config.TomlConfig, key string) string {
	tri := func(p *bool) string {
		if p == nil {
			return "default"
		}
		if *p {
			return "on"
		}
		return "off"
	}
	switch key {
	case "orchestrator":
		return tri(c.Orchestrator)
	case "thinking":
		return tri(c.Thinking)
	case "navigator":
		if strings.TrimSpace(c.Navigator) == "" {
			return "default"
		}
		return c.Navigator
	case "stable_toolset":
		return tri(c.StableToolset)
	case "cache_prompt":
		return tri(c.CachePrompt)
	case "darwin_parallel":
		return tri(c.DarwinParallel)
	case "task_parallel":
		return tri(c.TaskParallel)
	case "noop_gate":
		return tri(c.NoopGate)
	case "preflight_repo":
		return tri(c.PreflightRepo)
	case "draft_verify":
		return tri(c.DraftVerify)
	}
	return ""
}

func dashKnob(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// knobSet applies one wire value to the config. Tri-state knobs accept
// "on"/"off"/"default" (and "auto" as an alias for default); the
// navigator accepts "auto"/"on"/"off"; int knobs a non-negative number
// or "" (default); text knobs any string ("" = default). Live side
// effects match the TUI (thinking, cache_prompt).
func knobSet(c *config.TomlConfig, key, val string) error {
	val = strings.TrimSpace(val)
	setTri := func(p **bool) error {
		switch val {
		case "default", "auto", "":
			*p = nil
		case "on":
			v := true
			*p = &v
		case "off":
			v := false
			*p = &v
		default:
			return fmt.Errorf("bad value %q (want on/off/default)", val)
		}
		return nil
	}
	setInt := func(dst *int) error {
		if val == "" {
			*dst = 0
			return nil
		}
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("bad value %q (want a non-negative integer)", val)
		}
		*dst = n
		return nil
	}
	switch key {
	case "orchestrator":
		return setTri(&c.Orchestrator)
	case "thinking":
		if err := setTri(&c.Thinking); err != nil {
			return err
		}
		llm.SetThinkingEnabled(c.Thinking == nil || *c.Thinking)
		return nil
	case "navigator":
		switch val {
		case "auto", "default", "":
			c.Navigator = ""
		case "on", "off":
			c.Navigator = val
		default:
			return fmt.Errorf("bad value %q (want auto/on/off)", val)
		}
		return nil
	case "stable_toolset":
		return setTri(&c.StableToolset)
	case "cache_prompt":
		if err := setTri(&c.CachePrompt); err != nil {
			return err
		}
		llm.SetCachePromptDefault(c.CachePrompt)
		return nil
	case "darwin_parallel":
		return setTri(&c.DarwinParallel)
	case "task_parallel":
		return setTri(&c.TaskParallel)
	case "memory_briefing_tokens":
		return setInt(&c.MemoryBriefingTokens)
	case "task_max_steps":
		return setInt(&c.TaskMaxSteps)
	case "task_max_tokens":
		n := 0
		if err := setInt(&n); err != nil {
			return err
		}
		c.TaskMaxTokens = int64(n)
		return nil
	case "task_model":
		c.TaskModel = val
		return nil
	case "noop_gate":
		return setTri(&c.NoopGate)
	case "preflight_repo":
		return setTri(&c.PreflightRepo)
	case "draft_verify":
		return setTri(&c.DraftVerify)
	case "draft_verify_max_rounds":
		return setInt(&c.DraftVerifyMaxRounds)
	case "verify_commands":
		var out []string
		for _, part := range strings.Split(val, ";") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		c.VerifyCommands = out
		return nil
	}
	return fmt.Errorf("unknown or read-only setting %q", key)
}

// knobResetAll returns every managed knob to its default. Provider
// entries, API keys, hidden models and default_model/provider are left
// untouched — identical to the TUI's "reset all".
func knobResetAll(c *config.TomlConfig) {
	for _, d := range knobDefs() {
		if d.kind == knobReadonly {
			continue
		}
		_ = knobSet(c, d.key, "")
	}
}

// handleConfigKnobs is GET/POST /api/config: the /settings panel.
//
//	GET  -> {"knobs":[...]}
//	POST {"key":K,"value":V} -> set one knob ("" or "default" resets)
//	POST {"reset_all":true}  -> reset every managed knob
//
// Writes go to the GLOBAL config.toml (same file the TUI /settings,
// /think and /orchestrator persist to), loaded fresh per request so
// provider entries and keys written by other surfaces survive.
func (s *Server) handleConfigKnobs(w http.ResponseWriter, r *http.Request) {
	global, _ := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())

	if r.Method == http.MethodPost {
		var req struct {
			Key      string `json:"key"`
			Value    string `json:"value"`
			ResetAll bool   `json:"reset_all"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		// A load error means the file exists but is unreadable/corrupt
		// (a missing file loads as a clean zero config). Saving a zero
		// struct over it would silently wipe providers, API keys and
		// default_model — refuse instead.
		tc, err := config.LoadToml(global)
		if err != nil {
			http.Error(w, "load config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if req.ResetAll {
			knobResetAll(&tc)
		} else if err := knobSet(&tc, strings.TrimSpace(req.Key), req.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := config.SaveToml(global, tc); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	tc, err := config.LoadToml(global)
	if err != nil {
		tc = config.TomlConfig{}
	}
	out := make([]knobView, 0, len(knobDefs()))
	for _, d := range knobDefs() {
		value, source, raw := knobValue(&tc, d.key)
		out = append(out, knobView{
			Key: d.key, Label: d.key, Desc: d.desc, Kind: d.kind,
			Value: value, Source: source, Raw: raw,
			State: knobState(&tc, d.key), NextSession: d.nextSession,
		})
	}
	writeJSON(w, map[string]any{"knobs": out})
}
