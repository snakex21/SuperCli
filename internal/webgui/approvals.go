package webgui

import (
	"log"
	"path/filepath"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/tools"
)

// InstallApprovalGate makes the host approval gate the process approver for
// the web GUI. Each chat run routes its prompts through the run's ask channel
// (stream_run), so the approval card appears in that conversation, including
// branded overlays such as NestCafe that render generic question events. A
// request with no connected run (no UI) is denied unless the global
// [approvals] auto_approve allowlist covers it. allow_all/--allow-all never
// approves anything here: a GUI always has a human to ask.
func InstallApprovalGate(dataDir string) *tools.ApprovalGate {
	policy, err := config.LoadApprovals(dataDir)
	if err != nil {
		log.Printf("approvals: load [approvals] from config.toml: %v (prompting without an allowlist)", err)
	}
	gate := &tools.ApprovalGate{
		AutoApprove:          policy.AutoApprove,
		NonInteractiveReason: "no chat window is attached to this run",
		AuditPath:            filepath.Join(dataDir, "approvals.jsonl"),
		Language: func() string {
			cfg, _ := config.LoadToml(filepath.Join(dataDir, "config.toml"))
			return uilang.Resolve(cfg.Language)
		},
	}
	if policy.SessionGrantMinutes > 0 {
		gate.GrantTTL = time.Duration(policy.SessionGrantMinutes) * time.Minute
	}
	tools.SetDefaultApprover(gate)
	return gate
}
