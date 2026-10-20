package agent

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

type userInstructionsContextKey struct{}

// UserInstructionsFromContext returns the owning run's stable preference block.
// An empty value with ok=true explicitly means disabled; delegated tools must
// not reread a newer preset during that same run.
func UserInstructionsFromContext(ctx context.Context) (instructions string, ok bool) {
	instructions, ok = ctx.Value(userInstructionsContextKey{}).(string)
	return
}

func systemWithUserInstructions(base, instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return base
	}
	if base == "" {
		return instructions
	}
	return base + "\n\n" + instructions
}

// refreshUserInstructionsForRun runs under the conversation lease. Keeping the
// leading slot preserves hidden indices, other system layers and dialogue.
func (l *Loop) refreshUserInstructionsForRun(ctx context.Context) error {
	if l.userInstructionsForRun == nil {
		return nil
	}
	instructions, err := l.userInstructionsForRun(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	instructions = strings.TrimSpace(instructions)
	system := systemWithUserInstructions(l.baseSystem, instructions)
	if system == l.system {
		l.userInstructions = instructions
		return nil
	}
	if len(l.Messages) == 0 || l.Messages[0].Role != llm.RoleSystem {
		return fmt.Errorf("leading system prompt is missing")
	}
	l.system, l.userInstructions = system, instructions
	l.Messages[0] = llm.Message{Role: llm.RoleSystem, Content: system, Origin: &llm.ContextOrigin{Source: llm.SourceInstructions}}
	l.invalidateVisibleEstimate()
	return nil
}
