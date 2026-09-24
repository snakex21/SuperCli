package app

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/ui/tui"
)

func reasoningHistoryCommand(dataDir, cwd string) tui.SlashHandler {
	return func(_ context.Context, args string) (string, error) {
		args = strings.ToLower(strings.TrimSpace(args))
		mode := "keep"
		if llm.DiscardPreviousReasoning() {
			mode = "drop"
		}
		if args == "" {
			return fmt.Sprintf("reasoning history: %s\nusage: /reasoning-history <keep|drop|default>\nApplies from the next turn; the transcript and required tool-call reasoning are preserved.", mode), nil
		}
		var value *bool
		switch args {
		case "keep", "drop":
			v := args == "drop"
			value = &v
		case "default":
		default:
			return "usage: /reasoning-history <keep|drop|default>", nil
		}
		global, _ := config.FindTomlPaths(dataDir, cwd)
		tc, err := config.LoadToml(global)
		if err != nil {
			return "", fmt.Errorf("reasoning history: load config: %w", err)
		}
		tc.DiscardPreviousReasoning = value
		if err := config.SaveToml(global, tc); err != nil {
			return "", fmt.Errorf("reasoning history: save config: %w", err)
		}
		llm.SetDiscardPreviousReasoning(value != nil && *value)
		mode = "keep"
		if llm.DiscardPreviousReasoning() {
			mode = "drop"
		}
		return "reasoning history: " + mode + " from the next turn; transcript and required tool-call reasoning preserved", nil
	}
}
