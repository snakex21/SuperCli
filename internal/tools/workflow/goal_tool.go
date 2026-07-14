package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"supercli/internal/storage/goal"
)

// GoalTool exposes long-term goal state to the model.
// The model sees only this view; raw SQLite stays in the
// goal package.
//
// Schema:
//
//	{
//	  "action": "set" | "show" | "list" | "tasks" | "add_task" |
//	            "complete_task" | "skip_task" |
//	            "add_note" | "verify" | "mark_done" | "abandon" |
//	            "decompose",
//	  "goal_id":  "...",            // optional, defaults to active
//	  "task_seq": 2,                // for complete_task / skip_task
//	  "title":    "do the thing",   // for set, add_task, decompose (input)
//	  "description": "background",  // optional for set
//	  "success_criteria": "done",   // optional for set
//	  "context":  "background...",  // for decompose
//	  "text":     "note body"       // for add_note
//	}
//
// Returns a rendered Markdown string. The model is
// expected to surface the text to the user, not parse it.
type GoalTool struct {
	// Service is the in-memory front of the goal package.
	// Required.
	Service *goal.Service

	// Now is injected for tests. Default: time.Now.
	Now func() time.Time

	// DecomposeProvider is optional. When nil, GoalTool
	// falls back to goal.HeuristicFromTitle for the
	// "decompose" action.
	DecomposeProvider goal.Provider
	// DecomposeModel is the model id passed to the
	// provider. Empty uses the provider's default name.
	DecomposeModel string
}

// NewGoalTool wires a goal tool to a service.
func NewGoalTool(svc *goal.Service) *GoalTool {
	return &GoalTool{Service: svc, Now: time.Now}
}

// SetNow is for tests.
func (g *GoalTool) SetNow(f func() time.Time) { g.Now = f }

// SetDecompose wires an LLM provider for model-driven
// decomposition. nil disables it.
func (g *GoalTool) SetDecompose(p goal.Provider, model string) {
	g.DecomposeProvider = p
	g.DecomposeModel = model
}

// Spec returns the Tool descriptor.
func (g *GoalTool) Spec() Tool {
	return Tool{
		Name: "goal",
		Description: "Track a long-term objective and its task list. The active goal and its open tasks are injected into every turn, so keeping them accurate gives the user real visibility into your progress.\n\n" +
			"## When to use this tool\n" +
			"- Multi-step work (3+ distinct steps) or work spanning multiple turns/sessions\n" +
			"- The user gives several tasks at once, or asks you to plan or track work\n" +
			"- You discover new required work mid-task: add_task it immediately so it is not lost\n\n" +
			"## When NOT to use this tool\n" +
			"- Single, trivial, or purely conversational requests — just do them\n" +
			"- Durable facts or preferences (use the remember tool instead)\n\n" +
			"## Task management rules\n" +
			"- Mark a task in_progress (start_task) BEFORE you begin working on it, not after\n" +
			"- Keep exactly ONE task in_progress at a time; finish or skip it before starting the next\n" +
			"- Mark tasks complete (complete_task) IMMEDIATELY when done — do not batch completions\n" +
			"- Never complete a task while tests fail or the work is partial; keep it in_progress and add_note what is blocking\n\n" +
			"## Completion rules\n" +
			"- After every task is done or deliberately skipped, verify the result against success_criteria (or the goal title)\n" +
			"- Call verify with passed=true only after concrete checks; evidence must name those checks and results\n" +
			"- If verification fails, record passed=false, reopen or add the needed task, fix it, and verify again\n" +
			"- mark_done is rejected until the latest verification passes\n\n" +
			"Actions: set (create and activate a goal), show, list, tasks, add_task, start_task, complete_task, skip_task, add_note, verify, mark_done (close a verified goal), abandon, decompose (break the goal title into 3-7 tasks). Returns Markdown to surface to the user.",
		Schema: `{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["set", "show", "list", "tasks", "add_task", "start_task", "complete_task",
					         "skip_task", "add_note", "verify", "mark_done", "abandon", "decompose"],
					"description": "What to do."
				},
				"goal_id":  {"type": "string", "description": "Goal id (g-...). Defaults to the active goal."},
				"task_seq": {"type": "integer", "description": "Task sequence number (1-based)."},
				"title":    {"type": "string", "description": "For set / add_task / decompose input."},
				"description": {"type": "string", "description": "Optional background for set."},
				"success_criteria": {"type": "string", "description": "Optional definition of done for set."},
				"context":  {"type": "string", "description": "Optional context for decompose."},
				"text":     {"type": "string", "description": "Note body for add_note; concrete evidence for verify."},
				"passed":   {"type": "boolean", "description": "Required for verify. True only when concrete checks satisfy the goal."}
			},
			"required": ["action"]
		}`,
		Fn: g.Execute,
	}
}

type goalParams struct {
	Action          string `json:"action"`
	GoalID          string `json:"goal_id,omitempty"`
	TaskSeq         int    `json:"task_seq,omitempty"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	SuccessCriteria string `json:"success_criteria,omitempty"`
	Context         string `json:"context,omitempty"`
	Text            string `json:"text,omitempty"`
	Passed          *bool  `json:"passed,omitempty"`
}

func (p goalParams) Validate() error {
	switch p.Action {
	case "show", "list", "tasks":
		return nil
	case "set":
		if strings.TrimSpace(p.Title) == "" {
			return fmt.Errorf("goal: set requires title")
		}
		return nil
	case "add_task":
		if strings.TrimSpace(p.Title) == "" {
			return fmt.Errorf("goal: add_task requires title")
		}
		return nil
	case "start_task", "complete_task", "skip_task":
		if p.TaskSeq <= 0 {
			return fmt.Errorf("goal: %s requires task_seq", p.Action)
		}
		return nil
	case "add_note":
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("goal: add_note requires text")
		}
		return nil
	case "verify":
		if p.Passed == nil {
			return fmt.Errorf("goal: verify requires passed")
		}
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("goal: verify requires evidence in text")
		}
		return nil
	case "mark_done", "abandon":
		return nil
	case "decompose":
		if strings.TrimSpace(p.Title) == "" {
			return fmt.Errorf("goal: decompose requires title")
		}
		return nil
	default:
		return fmt.Errorf("goal: unknown action %q", p.Action)
	}
}

// Execute runs the action. Errors are returned in the
// Result.Err field; the second return is reserved for
// Go-level panics (none expected).
func (g *GoalTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if g.Service == nil {
		return Result{Err: fmt.Errorf("goal: service not configured")},
			fmt.Errorf("goal: service not configured")
	}
	var p goalParams
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("goal: bad args: %w", err)},
			fmt.Errorf("goal: bad args: %w", err)
	}
	if err := p.Validate(); err != nil {
		return Result{Err: err}, err
	}

	switch p.Action {
	case "set":
		return g.execSet(ctx, p.Title, p.Description, p.SuccessCriteria)
	case "show":
		return g.execShow(ctx, p.GoalID)
	case "list":
		return g.execList(ctx)
	case "tasks":
		return g.execTasks(ctx, p.GoalID)
	case "add_task":
		return g.execAddTask(ctx, p.GoalID, p.Title)
	case "start_task":
		return g.execSetTaskStatus(ctx, p.GoalID, p.TaskSeq, goal.TaskInProgress)
	case "complete_task":
		return g.execSetTaskStatus(ctx, p.GoalID, p.TaskSeq, goal.TaskDone)
	case "skip_task":
		return g.execSetTaskStatus(ctx, p.GoalID, p.TaskSeq, goal.TaskSkipped)
	case "add_note":
		return g.execAddNote(ctx, p.GoalID, p.Text)
	case "verify":
		return g.execVerify(ctx, p.GoalID, *p.Passed, p.Text)
	case "mark_done":
		return g.execSetGoalStatus(ctx, p.GoalID, goal.StatusDone)
	case "abandon":
		return g.execSetGoalStatus(ctx, p.GoalID, goal.StatusAbandoned)
	case "decompose":
		return g.execDecompose(ctx, p.GoalID, p.Title, p.Context)
	}
	return Result{Err: errors.New("goal: unreachable")}, nil
}

func (g *GoalTool) execSet(ctx context.Context, title, description, criteria string) (Result, error) {
	gl, err := g.Service.Set(ctx, title, strings.TrimSpace(description), strings.TrimSpace(criteria), "")
	if err != nil {
		return Result{Err: err}, err
	}
	return Result{Text: fmt.Sprintf("active goal: %s (%s)", gl.Title, gl.ID)}, nil
}

func (g *GoalTool) execShow(ctx context.Context, id string) (Result, error) {
	if id == "" {
		a := g.Service.Active()
		if a == nil {
			return Result{Text: "no active goal. Use /goal set <title>."}, nil
		}
		id = a.ID
	}
	gl, err := g.Service.Goal(ctx, id)
	if err != nil {
		return Result{Err: err}, err
	}
	if gl == nil {
		return Result{Err: fmt.Errorf("goal: %w: %s", goal.ErrNotFound, id)},
			fmt.Errorf("goal: %w: %s", goal.ErrNotFound, id)
	}
	return Result{Text: renderGoal(*gl)}, nil
}

func (g *GoalTool) execList(ctx context.Context) (Result, error) {
	all, err := g.Service.List(ctx)
	if err != nil {
		return Result{Err: err}, err
	}
	if len(all) == 0 {
		return Result{Text: "no goals yet. Use /goal set <title>."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d goal(s):\n", len(all))
	for _, gl := range all {
		fmt.Fprintf(&b, "  %s  %-9s  %s  %s\n",
			gl.ID, gl.Status, shorten(gl.Title, 60), formatRel(gl.CreatedAt, g.Now()))
	}
	return Result{Text: b.String()}, nil
}

func (g *GoalTool) execTasks(ctx context.Context, id string) (Result, error) {
	if id == "" {
		a := g.Service.Active()
		if a == nil {
			return Result{Text: "no active goal."}, nil
		}
		id = a.ID
	}
	tasks, err := g.Service.ListTasks(ctx, id)
	if err != nil {
		return Result{Err: err}, err
	}
	if len(tasks) == 0 {
		return Result{Text: "no tasks for this goal."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d task(s):\n", len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(&b, "  [%s] %d. %s\n", statusMark(t.Status), t.Seq, t.Title)
	}
	return Result{Text: b.String()}, nil
}

func (g *GoalTool) execAddTask(ctx context.Context, id, title string) (Result, error) {
	if id == "" {
		a := g.Service.Active()
		if a == nil {
			return Result{Err: fmt.Errorf("goal: add_task: no active goal")},
				fmt.Errorf("goal: add_task: no active goal")
		}
		id = a.ID
	}
	t, err := g.Service.AddTask(ctx, id, title)
	if err != nil {
		return Result{Err: err}, err
	}
	return Result{Text: fmt.Sprintf("added task %d: %s", t.Seq, t.Title)}, nil
}

func (g *GoalTool) execSetTaskStatus(ctx context.Context, id string, seq int, status goal.Status) (Result, error) {
	if id == "" {
		a := g.Service.Active()
		if a == nil {
			return Result{Err: fmt.Errorf("goal: no active goal")},
				fmt.Errorf("goal: no active goal")
		}
		id = a.ID
	}
	if err := g.Service.SetTaskStatus(ctx, id, seq, status); err != nil {
		return Result{Err: err}, err
	}
	return Result{Text: fmt.Sprintf("task %d -> %s", seq, status)}, nil
}

func (g *GoalTool) execAddNote(ctx context.Context, id, text string) (Result, error) {
	if id == "" {
		a := g.Service.Active()
		if a == nil {
			return Result{Err: fmt.Errorf("goal: no active goal")},
				fmt.Errorf("goal: no active goal")
		}
		id = a.ID
	}
	if err := g.Service.AppendNote(ctx, id, text); err != nil {
		return Result{Err: err}, err
	}
	return Result{Text: fmt.Sprintf("note appended: %q", shorten(text, 60))}, nil
}

func (g *GoalTool) execVerify(ctx context.Context, id string, passed bool, evidence string) (Result, error) {
	if err := g.Service.Verify(ctx, id, passed, evidence); err != nil {
		return Result{Err: err}, err
	}
	status := "failed"
	if passed {
		status = "passed; goal can now be marked done"
	}
	return Result{Text: fmt.Sprintf("goal verification %s: %s", status, shorten(strings.TrimSpace(evidence), 160))}, nil
}

func (g *GoalTool) execSetGoalStatus(ctx context.Context, id string, status goal.Status) (Result, error) {
	if id == "" {
		a := g.Service.Active()
		if a == nil {
			return Result{Err: fmt.Errorf("goal: no active goal")},
				fmt.Errorf("goal: no active goal")
		}
		id = a.ID
	}
	if err := g.Service.SetStatus(ctx, id, status); err != nil {
		return Result{Err: err}, err
	}
	return Result{Text: fmt.Sprintf("goal %s -> %s", id, status)}, nil
}

func (g *GoalTool) execDecompose(ctx context.Context, id, title, contextDesc string) (Result, error) {
	var tasks []string
	if g.DecomposeProvider != nil {
		t, err := goal.ModelDecompose(ctx, g.DecomposeProvider, g.DecomposeModel, title, contextDesc)
		if err == nil && len(t) > 0 {
			tasks = t
		}
	}
	if len(tasks) == 0 {
		tasks = goal.HeuristicFromTitle(title)
	}
	if len(tasks) == 0 {
		return Result{Text: "no tasks produced"}, nil
	}
	// Persist to a goal: if id is empty, use active goal;
	// otherwise look it up. If none, return the proposed
	// list only.
	if id == "" {
		id = ""
		if a := g.Service.Active(); a != nil {
			id = a.ID
		}
	}
	if id == "" {
		return Result{Text: "proposed tasks (no goal to attach to):\n" + renderProposed(tasks)}, nil
	}
	for _, t := range tasks {
		if _, err := g.Service.AddTask(ctx, id, t); err != nil {
			return Result{Err: err}, err
		}
	}
	return Result{Text: fmt.Sprintf("added %d tasks to %s:\n%s", len(tasks), id, renderProposed(tasks))}, nil
}

// renderGoal formats one goal as Markdown.
func renderGoal(g goal.Goal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", g.Title)
	if g.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", g.Description)
	}
	if g.SuccessCriteria != "" {
		fmt.Fprintf(&b, "\n## success_criteria\n%s\n", g.SuccessCriteria)
	}
	if g.VerificationStatus != goal.VerificationNone {
		fmt.Fprintf(&b, "\n## verification\n%s: %s\n", g.VerificationStatus, g.VerificationEvidence)
	}
	fmt.Fprintf(&b, "\nid: %s\nstatus: %s\n", g.ID, g.Status)
	if g.Notes != "" {
		fmt.Fprintf(&b, "\n## notes\n%s\n", g.Notes)
	}
	return b.String()
}

func renderProposed(tasks []string) string {
	var b strings.Builder
	for i, t := range tasks {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, t)
	}
	return b.String()
}

func statusMark(s goal.Status) string {
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

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func formatRel(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "future"
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
