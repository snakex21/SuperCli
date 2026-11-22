package core

import (
	"errors"
	"fmt"
	"strings"

	"supercli/internal/tools/sandbox"
)

// ErrDestructiveCommand marks a command refused by the destructive command
// guard (sandbox.CheckCommand); it was never started.
var ErrDestructiveCommand = errors.New("blocked: destructive command")

// CommandApproval turns a destructive-command check into a tool approval
// decision: nil for an allowed command, an error for a blocked one (the
// command never runs), and an approval request when the user must decide.
func CommandApproval(tool string, argv []string, check sandbox.CommandCheck) (*ApprovalRequest, error) {
	switch check.Verdict {
	case sandbox.CommandBlock:
		return nil, SelfContainedErr(fmt.Errorf("%w (%s). It was not run. Never run it yourself and do not look for another way to do the same: "+
			"tell the user exactly what it would do and ask them to run it if they want it", ErrDestructiveCommand, check.Reason))
	case sandbox.CommandAsk:
		line := strings.Join(argv, " ")
		return &ApprovalRequest{
			Tool: tool, Operation: "destructive_command", Risk: RiskDestructive, Reversible: false,
			Target:         line,
			Summary:        fmt.Sprintf("Run a command that may delete files whose location is only known when it runs (%s)? %s", check.Reason, line),
			Scope:          []string{tool, "destructive_command"},
			Count:          -1,
			NoSessionGrant: true,
		}, nil
	}
	return nil, nil
}
