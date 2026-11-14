package app

import (
	"supercli/internal/assembly"
	"supercli/internal/tools"
)

// installApprovalGate makes the host approval gate the process approver.
// Risky tool operations (Tool.Approval) then need the user's answer in the TUI
// (ask channel), or, without a UI, the [approvals] auto_approve allowlist from
// the global data-dir config. allowAllNonInteractive is only the explicit
// --allow-all flag of a non-interactive run; it approves every gated action and
// is documented as risky.
func installApprovalGate(dataDir string, ask chan<- tools.AskRequest, language string, nonInteractiveReason string, allowAllNonInteractive bool) *tools.ApprovalGate {
	return assembly.InstallApprovalGate(assembly.GateSpec{
		DataDir:                dataDir,
		Fallback:               ask,
		Language:               func() string { return language },
		NonInteractiveReason:   nonInteractiveReason,
		AllowAllNonInteractive: allowAllNonInteractive,
	})
}
