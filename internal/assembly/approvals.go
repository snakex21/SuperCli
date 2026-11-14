package assembly

import (
	"log"
	"path/filepath"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// GateSpec is the per-surface approval policy.
type GateSpec struct {
	DataDir string
	// Fallback is the TUI ask channel. The web GUI routes prompts through
	// each run's context instead, and batch has no UI, so both leave it nil.
	Fallback chan<- tools.AskRequest
	// Language returns the UI language for prompt text. The CLI fixes it at
	// startup; the web GUI re-reads config.toml so a language switch in the
	// settings panel applies to the next approval card.
	Language func() string
	// NonInteractiveReason names the run kind in denial messages.
	NonInteractiveReason string
	// AllowAllNonInteractive is only the explicit --allow-all flag of a batch
	// run: it approves every gated action. A GUI always has a human to ask.
	AllowAllNonInteractive bool
}

// InstallApprovalGate builds the host approval gate from the global
// [approvals] policy (auto_approve allowlist, session_grant_minutes, audit
// log in the data directory) and makes it the process approver.
func InstallApprovalGate(spec GateSpec) *tools.ApprovalGate {
	policy, err := config.LoadApprovals(spec.DataDir)
	if err != nil {
		log.Printf("approvals: load [approvals] from config.toml: %v (prompting/denying without an allowlist)", err)
	}
	gate := &tools.ApprovalGate{
		Fallback:               spec.Fallback,
		AutoApprove:            policy.AutoApprove,
		AllowAllNonInteractive: spec.AllowAllNonInteractive,
		NonInteractiveReason:   spec.NonInteractiveReason,
		AuditPath:              filepath.Join(spec.DataDir, "approvals.jsonl"),
		Language:               spec.Language,
	}
	if policy.SessionGrantMinutes > 0 {
		gate.GrantTTL = time.Duration(policy.SessionGrantMinutes) * time.Minute
	}
	if spec.AllowAllNonInteractive {
		log.Printf("approvals: --allow-all in a non-interactive run approves every gated action without asking")
	}
	tools.SetDefaultApprover(gate)
	return gate
}
