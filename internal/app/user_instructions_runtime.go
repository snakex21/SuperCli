package app

import (
	"context"

	"supercli/internal/llm/prompt"
)

// userInstructionsForRun snapshots a preset once per user run, not once per
// tool/model step. Malformed or unreadable configuration fails before sending
// a request instead of silently dropping instructions the user enabled.
func userInstructionsForRun(dataDir string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		return prompt.UserInstructionsForRun(ctx, dataDir)
	}
}
