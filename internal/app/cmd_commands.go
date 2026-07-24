package app

import (
	"context"
	"encoding/json"
	"strings"

	"supercli/internal/agent/darwin"
	"supercli/internal/storage/goal"
	"supercli/internal/ui/tui"
)

// darwinCommands returns the slash-command table for the
// TUI. Currently registers `/darwin` which invokes the
// DarwinTool synchronously and returns the rendered
// result. dt is the same DarwinTool instance registered
// in the tool registry, so the slash command and the
// model-invoked tool share state.
func darwinCommands(dt *darwin.DarwinTool) map[string]tui.SlashHandler {
	return map[string]tui.SlashHandler{
		"darwin": func(ctx context.Context, args string) (string, error) {
			prompt, poolSize := parseDarwinArgs(args, 3)
			if strings.TrimSpace(prompt) == "" {
				return "usage: /darwin [N] <prompt>\nexample: /darwin 3 fix failing tests", nil
			}
			raw, err := json.Marshal(map[string]any{
				"prompt":     prompt,
				"pool_size":  poolSize,
				"auto_merge": false,
				"judge":      "composite",
			})
			if err != nil {
				return "", err
			}
			res, _ := dt.Spec().Fn(ctx, raw)
			if res.Err != nil {
				return "", res.Err
			}
			return res.Text, nil
		},
	}
}

// goalCommands returns the `/goal` slash-command
// handler. The subcommand (set/list/show/tasks/...) is
// the first whitespace-separated token of args; the
// rest is passed to that action.
//
// Examples:
//
//	/goal set ship F8
//	/goal show
//	/goal tasks add design doc
//	/goal tasks done 1
//	/goal note we have a draft
//	/goal verify pass go test ./... passed
//	/goal done
//
// All mutations call Refresh on the service so the
// status line and the next injected prompt see the
// new state.
func goalCommands(svc *goal.Service) map[string]tui.SlashHandler {
	run := func(_ context.Context, args string) (string, error) {
		return runGoalCommand(svc, args)
	}
	return map[string]tui.SlashHandler{
		"goal": run,
	}
}

// runGoalCommand parses "/goal <subcmd> [args...]" and
// dispatches to the right goal.Service / tools.GoalTool
// action. Returns a Markdown string the TUI prints.
