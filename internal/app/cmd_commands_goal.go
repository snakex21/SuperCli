package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"supercli/internal/agent/darwin"
	"supercli/internal/storage/goal"
	"supercli/internal/ui/tui"
	"supercli/internal/verification/hardtest"
)

func runGoalCommand(svc *goal.Service, args string) (string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return goalUsage(), nil
	}
	fields := strings.Fields(args)
	sub := strings.ToLower(fields[0])
	rest := ""
	if len(fields) > 1 {
		rest = strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	}
	ctx := context.Background()

	switch sub {
	case "set":
		title := rest
		if title == "" {
			return "goal: /goal set requires a title", nil
		}
		g, err := svc.Set(ctx, title, "", "", "")
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("active goal: %s (%s)", g.Title, g.ID), nil

	case "list":
		all, err := svc.List(ctx)
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		if len(all) == 0 {
			return "no goals yet. /goal set <title>", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d goal(s):\n", len(all))
		for _, g := range all {
			fmt.Fprintf(&b, "  %s  %-9s  %s\n", g.ID, g.Status, shortenLine(g.Title, 60))
		}
		return b.String(), nil

	case "show":
		id := rest
		g, err := resolveGoal(svc, ctx, id)
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s  [%s]  %s\n", g.ID, g.Status, g.Title)
		if g.Description != "" {
			fmt.Fprintf(&b, "  %s\n", g.Description)
		}
		if g.SuccessCriteria != "" {
			fmt.Fprintf(&b, "  criteria: %s\n", g.SuccessCriteria)
		}
		if g.VerificationStatus != goal.VerificationNone {
			fmt.Fprintf(&b, "  verification: %s — %s\n", g.VerificationStatus, shortenLine(g.VerificationEvidence, 200))
		}
		if g.Notes != "" {
			fmt.Fprintf(&b, "  notes: %s\n", shortenLine(g.Notes, 200))
		}
		return b.String(), nil

	case "tasks":
		// sub-sub: add, done, skip, list
		return runGoalTasks(svc, rest)

	case "decompose":
		title := rest
		if title == "" {
			return "goal: /goal decompose <title>", nil
		}
		tasks := goal.HeuristicFromTitle(title)
		if len(tasks) == 0 {
			return "goal: no tasks produced", nil
		}
		for _, t := range tasks {
			if _, err := svc.AddTask(ctx, "", t); err != nil {
				return "goal: " + err.Error(), nil
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "decomposed into %d tasks:\n", len(tasks))
		for i, t := range tasks {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, t)
		}
		return b.String(), nil

	case "note":
		text := rest
		if text == "" {
			return "goal: /goal note <text>", nil
		}
		if err := svc.AppendNote(ctx, "", text); err != nil {
			return "goal: " + err.Error(), nil
		}
		return "note appended.", nil

	case "verify":
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return "goal: /goal verify <pass|fail> <evidence>", nil
		}
		var passed bool
		switch strings.ToLower(strings.TrimSpace(parts[0])) {
		case "pass", "passed", "ok":
			passed = true
		case "fail", "failed":
			passed = false
		default:
			return "goal: /goal verify <pass|fail> <evidence>", nil
		}
		if err := svc.Verify(ctx, "", passed, strings.TrimSpace(parts[1])); err != nil {
			return "goal: " + err.Error(), nil
		}
		if passed {
			return "goal verification passed; the goal can now be marked done.", nil
		}
		return "goal verification failed; reopen or add the required task before retrying.", nil

	case "done":
		if err := svc.SetStatus(ctx, "", goal.StatusDone); err != nil {
			return "goal: " + err.Error(), nil
		}
		return "active goal marked done.", nil

	case "abandon":
		if err := svc.SetStatus(ctx, "", goal.StatusAbandoned); err != nil {
			return "goal: " + err.Error(), nil
		}
		return "active goal abandoned.", nil

	case "help", "?":
		return goalUsage(), nil

	default:
		return goalUsage(), nil
	}
}

// runGoalTasks handles "/goal tasks <add|done|skip|list>".
func runGoalTasks(svc *goal.Service, args string) (string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		// default: list
		args = "list"
	}
	fields := strings.Fields(args)
	sub := strings.ToLower(fields[0])
	rest := ""
	if len(fields) > 1 {
		rest = strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	}
	ctx := context.Background()

	switch sub {
	case "list":
		tasks, err := svc.ListTasks(ctx, "")
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		if len(tasks) == 0 {
			return "no tasks for active goal.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d task(s):\n", len(tasks))
		for _, t := range tasks {
			fmt.Fprintf(&b, "  [%s] %d. %s\n", taskMark(t.Status), t.Seq, t.Title)
		}
		return b.String(), nil

	case "add":
		if rest == "" {
			return "goal: /goal tasks add <title>", nil
		}
		t, err := svc.AddTask(ctx, "", rest)
		if err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("added task %d: %s", t.Seq, t.Title), nil

	case "done":
		if rest == "" {
			return "goal: /goal tasks done <seq>", nil
		}
		seq, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return "goal: invalid seq: " + rest, nil
		}
		if err := svc.SetTaskStatus(ctx, "", seq, goal.TaskDone); err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("task %d -> done", seq), nil

	case "skip":
		if rest == "" {
			return "goal: /goal tasks skip <seq>", nil
		}
		seq, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return "goal: invalid seq: " + rest, nil
		}
		if err := svc.SetTaskStatus(ctx, "", seq, goal.TaskSkipped); err != nil {
			return "goal: " + err.Error(), nil
		}
		return fmt.Sprintf("task %d -> skipped", seq), nil

	default:
		return "goal: tasks subcommand must be list, add, done, or skip", nil
	}
}

// mergedSlashCommands returns darwin + goal commands.
// Goal gets priority on key collision (defensive; they
// don't currently share keys).
func mergedSlashCommands(dt *darwin.DarwinTool, svc *goal.Service, home string) map[string]tui.SlashHandler {
	out := darwinCommands(dt)
	for k, v := range goalCommands(svc) {
		out[k] = v
	}
	out["test"] = func(ctx context.Context, args string) (string, error) {
		if strings.TrimSpace(args) != "hard" {
			return "usage: /test hard", nil
		}
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		report, err := hardtest.Run(runCtx, home)
		if err != nil {
			return "", err
		}
		return hardtest.Markdown(report), nil
	}
	return out
}

func goalUsage() string {
	return "usage: /goal <set|list|show|tasks|note|verify|done|abandon|decompose|help> [args]\n" +
		"  set <title>             start a new active goal (auto-pauses prior)\n" +
		"  list                    show all goals\n" +
		"  show [id]               show one goal (default: active)\n" +
		"  tasks [list]            list active goal's tasks (default)\n" +
		"  tasks add <title>       append a task\n" +
		"  tasks done <seq>        mark task done\n" +
		"  tasks skip <seq>        skip a task\n" +
		"  note <text>             append a timestamped note\n" +
		"  verify pass <evidence>  record successful final verification\n" +
		"  verify fail <evidence>  record a failed final verification\n" +
		"  done                    close a successfully verified goal\n" +
		"  abandon                 abandon the active goal\n" +
		"  decompose <title>       split a title into tasks (heuristic)"
}

func resolveGoal(svc *goal.Service, ctx context.Context, id string) (*goal.Goal, error) {
	if id == "" {
		g := svc.Active()
		if g == nil {
			return nil, fmt.Errorf("no active goal")
		}
		return g, nil
	}
	return svc.Goal(ctx, id)
}

func taskMark(s goal.Status) string {
	switch s {
	case goal.TaskDone:
		return "x"
	case goal.TaskInProgress:
		return ">"
	case goal.TaskSkipped:
		return "~"
	default:
		return " "
	}
}

func shortenLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// parseDarwinArgs splits "/darwin 3 fix the bug" into
// (prompt="fix the bug", poolSize=3). Default pool is 3.
func parseDarwinArgs(args string, defaultPool int) (string, int) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", defaultPool
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", defaultPool
	}
	if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
		return strings.TrimSpace(strings.TrimPrefix(args, fields[0])), n
	}
	return args, defaultPool
}
