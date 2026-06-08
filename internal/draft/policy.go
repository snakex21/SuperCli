// policy.go defines the F11 draft policy: which mode
// (always / balanced / critical-only), which model, and
// the exclude list. The policy is immutable after
// construction; the loop reads it on every step but
// never mutates it.

package draft

import (
	"errors"
	"fmt"
	"strings"
)

// Mode is how aggressive the draft bridge is.
//
//   - ModeAlways: draft on every step (most spend, most
//     consistency).
//   - ModeBalanced: draft on the first step only (the
//     user-facing default; saves tokens on follow-ups).
//   - ModeCriticalOnly: draft only on steps where the
//     model is about to invoke a "critical" tool
//     (file write, bash, MCP). Saves the most tokens
//     but the verifier may not see a draft for read
//     steps.
//   - ModeOff: no drafts. The bridge is a no-op.
type Mode int

const (
	// ModeOff disables F11 entirely. LoopConfig.Draft
	// == nil OR Policy.Mode == ModeOff → no draft
	// calls, no events.
	ModeOff Mode = iota
	// ModeAlways drafts on every step.
	ModeAlways
	// ModeBalanced drafts on step 0 only (first turn).
	// The "draft once, then verify" pattern.
	ModeBalanced
	// ModeCriticalOnly drafts only on steps where
	// the model emits a tool call to a tool whose
	// name appears in CriticalTools.
	ModeCriticalOnly
)

// String returns a stable lowercase name for each mode
// (e.g. "off", "always", "balanced", "critical_only").
// Used in log lines and the [draft: ...] marker.
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeAlways:
		return "always"
	case ModeBalanced:
		return "balanced"
	case ModeCriticalOnly:
		return "critical_only"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// ParseMode is the inverse of String. Empty input
// returns ModeOff. Unknown names return an error.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off":
		return ModeOff, nil
	case "always":
		return ModeAlways, nil
	case "balanced":
		return ModeBalanced, nil
	case "critical", "critical_only", "critical-only":
		return ModeCriticalOnly, nil
	default:
		return ModeOff, fmt.Errorf("draft: unknown mode %q (want off|always|balanced|critical)", s)
	}
}

// Policy is the immutable F11 configuration passed to
// the loop via LoopConfig.Draft. Build with NewPolicy.
type Policy struct {
	// Mode controls when drafts fire (see Mode).
	Mode Mode

	// Model is the draft model id (e.g.
	// "claude-haiku-4-5"). When empty, the bridge
	// resolves a model from the registry at the
	// first call; the resolved id is cached.
	Model string

	// MainModel is the verifier model id. The
	// registry must NOT pick this one as the draft
	// (a model doesn't draft itself).
	MainModel string

	// CriticalTools is the set of tool names that
	// trigger a draft in ModeCriticalOnly. The
	// default (when nil) is {"write_file",
	// "edit_file", "bash", "mcp__*"}. The actual
	// "is this a critical tool?" check is done
	// by IsCritical.
	CriticalTools []string

	// Drafted is the set of step indices at which
	// a draft was already injected. The bridge
	// consults this in ModeBalanced to skip steps
	// after the first.
	//
	// This is a pointer-to-mutable-state; NewPolicy
	// initializes an empty map. The loop shares
	// this map with the bridge via a pointer in
	// LoopConfig.
	Drafted *map[int]struct{}
}

// NewPolicy validates and constructs a Policy. Empty
// mode defaults to ModeOff (safer than guessing). A
// non-empty Model that does NOT differ from MainModel
// is rejected — that would be a degenerate draft.
func NewPolicy(mode Mode, draftModel, mainModel string, criticalTools []string) (*Policy, error) {
	if mainModel == "" {
		return nil, errors.New("draft.NewPolicy: mainModel is empty")
	}
	if mode == ModeOff {
		// Off-policy is still valid; just a
		// placeholder. Caller can pass nil
		// instead and skip construction.
		return &Policy{
			Mode:       ModeOff,
			MainModel:  mainModel,
			Drafted:    &map[int]struct{}{},
		}, nil
	}
	if draftModel != "" && draftModel == mainModel {
		return nil, fmt.Errorf("draft.NewPolicy: draftModel %q equals mainModel", draftModel)
	}
	tools := criticalTools
	if tools == nil {
		tools = defaultCriticalTools()
	}
	return &Policy{
		Mode:          mode,
		Model:         draftModel,
		MainModel:     mainModel,
		CriticalTools: tools,
		Drafted:       &map[int]struct{}{},
	}, nil
}

// ShouldDraft answers "should this step get a draft?"
// given the step index and the set of tool names the
// model is about to invoke (may be nil/empty for pure
// text steps).
//
// Returns (yes, reason). Reason is a short human-
// readable string suitable for log lines. reason is
// "off" / "already-drafted" / "not-critical" / "ok".
//
// For ModeBalanced, "already-drafted" fires when ANY
// step has been marked (not just this exact step).
// The intent is "draft once, then verify" — after
// the first plan is in, follow-up turns should run
// plain.
func (p *Policy) ShouldDraft(step int, upcomingTools []string) (bool, string) {
	if p == nil || p.Mode == ModeOff {
		return false, "off"
	}
	switch p.Mode {
	case ModeAlways:
		return true, "always"
	case ModeBalanced:
		if p.Drafted == nil {
			return true, "balanced"
		}
		if len(*p.Drafted) > 0 {
			return false, "already-drafted"
		}
		return true, "balanced"
	case ModeCriticalOnly:
		if len(upcomingTools) == 0 {
			return false, "no-tool-call"
		}
		for _, name := range upcomingTools {
			if p.IsCritical(name) {
				return true, "critical-tool"
			}
		}
		return false, "not-critical"
	}
	return false, "unknown-mode"
}

// MarkDrafted records that step had a draft injected.
// Idempotent. Safe on a nil Policy (no-op).
func (p *Policy) MarkDrafted(step int) {
	if p == nil || p.Drafted == nil {
		return
	}
	(*p.Drafted)[step] = struct{}{}
}

// IsCritical reports whether the given tool name is in
// the critical set. The match is exact, but MCP tools
// (prefix "mcp__") are always critical. Case-sensitive
// (tool names are conventionally kebab/snake and we
// don't want "Bash" matching "bash").
func (p *Policy) IsCritical(toolName string) bool {
	if p == nil {
		return false
	}
	if strings.HasPrefix(toolName, "mcp__") {
		return true
	}
	for _, c := range p.CriticalTools {
		if c == toolName {
			return true
		}
	}
	return false
}

// defaultCriticalTools is the F11 default set for
// ModeCriticalOnly. Returned as a fresh slice each
// call so callers can mutate without affecting other
// policies.
func defaultCriticalTools() []string {
	return []string{
		"write_file",
		"edit_file",
		"bash",
		"run_command",
		"write_docx",
		"write_xlsx",
		"write_pdf",
		"send_screenshot",
	}
}
