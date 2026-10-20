package agent

import (
	"context"
	"strings"
)

// workerInstructions pins the coordinator's selected preset, not its private
// system prompt or mutable settings callback. An explicit empty snapshot means
// disabled. Continuations can replace it at their next Run boundary; steering
// never changes instructions underneath an active provider request.
func (a *AgentTool) workerInstructions(ctx context.Context, system string) (string, string, func(context.Context) (string, error)) {
	if system != "" && a.ParentLoop != nil && a.ParentLoop.projectInstructions != "" {
		system += "\n\n" + a.ParentLoop.projectInstructions
	}
	instructions, selected := UserInstructionsFromContext(ctx)
	if !selected && a.ParentLoop != nil {
		instructions = a.ParentLoop.userInstructions
		selected = a.ParentLoop.userInstructionsForRun != nil || instructions != ""
	}
	if system == "" && a.ParentLoop != nil {
		system = a.ParentLoop.system
		if a.ParentLoop.baseSystem != "" || a.ParentLoop.userInstructions != "" {
			system = a.ParentLoop.baseSystem // do not bake the old preset into the child base
		}
	}
	if !selected {
		return system, "", nil
	}
	if strings.TrimSpace(system) == "" {
		// A custom role may inherit a coordinator made only of user preferences.
		// Keep a leading base slot so later disabling/replacing them is valid.
		system = "You are a SuperCli worker. Follow the assigned task."
	}
	current := strings.TrimSpace(instructions)
	initial := current
	return system, initial, func(ctx context.Context) (string, error) {
		// Called only under this child's conversation lease, including resume.
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if next, ok := UserInstructionsFromContext(ctx); ok {
			current = strings.TrimSpace(next)
		}
		return current, nil
	}
}
