package app

import (
	"log"
	"path/filepath"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// installApprovalGate makes the host approval gate the process approver.
// Risky tool operations (Tool.Approval) then need the user's answer in the TUI
// (ask channel), or, without a UI, the [approvals] auto_approve allowlist from
// the global data-dir config. allowAllNonInteractive is only the explicit
// --allow-all flag of a non-interactive run; it approves every gated action and
// is documented as risky.
func installApprovalGate(dataDir string, ask chan<- tools.AskRequest, language string, nonInteractiveReason string, allowAllNonInteractive bool) *tools.ApprovalGate {
	policy, err := config.LoadApprovals(dataDir)
	if err != nil {
		log.Printf("approvals: load [approvals] from config.toml: %v (prompting/denying without an allowlist)", err)
	}
	gate := &tools.ApprovalGate{
		Fallback:               ask,
		AutoApprove:            policy.AutoApprove,
		AllowAllNonInteractive: allowAllNonInteractive,
		NonInteractiveReason:   nonInteractiveReason,
		AuditPath:              filepath.Join(dataDir, "approvals.jsonl"),
		Language:               func() string { return language },
	}
	if policy.SessionGrantMinutes > 0 {
		gate.GrantTTL = time.Duration(policy.SessionGrantMinutes) * time.Minute
	}
	if allowAllNonInteractive {
		log.Printf("approvals: --allow-all in a non-interactive run approves every gated action without asking")
	}
	tools.SetDefaultApprover(gate)
	return gate
}
