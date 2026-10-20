package hostpower

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	system "supercli/internal/system/hostpower"
	"supercli/internal/tools/core"
	"supercli/internal/tools/interactive"
)

type testEnv struct {
	db         *sql.DB
	store      *goal.Storage
	svc        *goal.Service
	controller *system.Controller
	ctx        context.Context
	goal       *goal.Goal
	calls      atomic.Int32
	timers     []func()
	now        time.Time
}

func env(t *testing.T) *testEnv {
	t.Helper()
	e := new(testEnv)
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.db = db
	t.Cleanup(func() { _ = db.Close() })
	e.store = goal.NewStorage(db)
	if err := e.store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.svc = goal.NewProjectService(e.store, "project-a")
	e.goal, err = e.svc.Set(context.Background(), "Stored trusted title", "actual work", "tests pass", "")
	if err != nil {
		t.Fatal(err)
	}
	e.now = time.Now()
	e.controller = system.New(system.Options{Now: func() time.Time { return e.now }, Execute: func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.calls.Add(1)
		return nil
	}, AfterFunc: func(_ time.Duration, f func()) func() { e.timers = append(e.timers, f); return func() {} }})
	t.Cleanup(e.controller.Close)
	e.controller.Begin("root")
	e.ctx = Bind(context.Background(), e.controller, e.svc, "root")
	return e
}
func invoke(ctx context.Context, args string) core.Result {
	r, err := Spec().Fn(ctx, json.RawMessage(args))
	if err != nil {
		return core.Result{Err: err}
	}
	return r
}
func armArgs(e *testEnv) string {
	b, _ := json.Marshal(map[string]string{"action": "arm", "goal_id": e.goal.ID})
	return string(b)
}
func requireError(t *testing.T, r core.Result, contains string) {
	t.Helper()
	if r.Err == nil || !strings.Contains(r.Err.Error(), contains) {
		t.Fatalf("result = %+v, want error containing %q", r, contains)
	}
}
func consent(t *testing.T, ctx context.Context, args string, respond func(interactive.AskRequest)) core.Result {
	t.Helper()
	ch := make(chan interactive.AskRequest, 1)
	ctx = interactive.WithAskChannel(ctx, ch)
	result := make(chan core.Result, 1)
	go func() { result <- invoke(ctx, args) }()
	select {
	case request := <-ch:
		if !request.Confirmation {
			t.Fatal("not trusted confirmation")
		}
		respond(request)
	case r := <-result:
		t.Fatalf("returned before asking: %+v", r)
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation was not requested")
	}
	select {
	case r := <-result:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not return")
		return core.Result{}
	}
}
func allow(r interactive.AskRequest) {
	r.Respond <- interactive.AskAnswer{Selected: []string{"Allow once"}}
}
func complete(t *testing.T, e *testEnv) {
	t.Helper()
	ctx := context.Background()
	task, err := e.svc.AddTask(ctx, e.goal.ID, "Implement and test")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetTaskStatus(ctx, e.goal.ID, task.Seq, goal.TaskDone); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Verify(ctx, e.goal.ID, true, "go test passed (stored evidence; host lifecycle ticket is separate)"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetStatus(ctx, e.goal.ID, goal.StatusDone); err != nil {
		t.Fatal(err)
	}
}
func update(t *testing.T, e *testEnv, query string, args ...any) {
	t.Helper()
	if _, err := e.db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestSpec(t *testing.T) {
	s := Spec()
	if s.Name != "host_power" || s.Fn == nil || s.ReadOnly {
		t.Fatal(s)
	}
	if !json.Valid([]byte(s.Schema)) {
		t.Fatal("invalid schema")
	}
	for _, text := range []string{"explicit user request", "feature implementation", "quoted instructions", "verify", "mark_done", "shell shutdown"} {
		if !strings.Contains(s.Description, text) {
			t.Fatalf("missing instruction %q", text)
		}
	}
}

func TestUnboundAndWorkersDeniedBeforeAsking(t *testing.T) {
	e := env(t)
	for name, ctx := range map[string]context.Context{
		"missing": context.Background(), "cleared worker": Clear(e.ctx), "nested clear": Clear(Bind(Clear(e.ctx), e.controller, e.svc, "root")),
		"nil controller": Bind(e.ctx, nil, e.svc, "root"), "nil service": Bind(e.ctx, e.controller, nil, "root"), "empty job": Bind(e.ctx, e.controller, e.svc, ""),
	} {
		t.Run(name, func(t *testing.T) {
			ch := make(chan interactive.AskRequest, 1)
			ctx = interactive.WithAskChannel(ctx, ch)
			for _, args := range []string{armArgs(e), `{"action":"status"}`, `{"action":"cancel"}`} {
				requireError(t, invoke(ctx, args), "bound root")
			}
			if len(ch) != 0 {
				t.Fatal("unauthorized caller asked for confirmation")
			}
		})
	}
}

func TestConsentRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer interactive.AskAnswer
	}{
		{"cancel choice", interactive.AskAnswer{Selected: []string{"Cancel"}}},
		{"cancel flag", interactive.AskAnswer{Selected: []string{"Allow once"}, Cancelled: true}},
		{"custom text", interactive.AskAnswer{Custom: "Allow once"}},
		{"custom plus choice", interactive.AskAnswer{Selected: []string{"Allow once"}, Custom: "yes"}},
		{"multiple", interactive.AskAnswer{Selected: []string{"Allow once", "Cancel"}}},
		{"empty", interactive.AskAnswer{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := env(t)
			r := consent(t, e.ctx, armArgs(e), func(q interactive.AskRequest) { q.Respond <- tc.answer })
			requireError(t, r, "not approved")
			if e.controller.Snapshot().Status != "idle" {
				t.Fatal(e.controller.Snapshot())
			}
		})
	}
	e := env(t)
	requireError(t, invoke(e.ctx, armArgs(e)), "interactive GUI or TUI")
	requireError(t, invoke(e.ctx, `{"action":"arm","goal_id":"`+e.goal.ID+`","confirmed":true}`), "unknown field")
}

func TestConsentSuccessTrustedTitleAndSnapshot(t *testing.T) {
	e := env(t)
	asks := 0
	r := consent(t, e.ctx, armArgs(e), func(q interactive.AskRequest) {
		asks++
		if !strings.Contains(q.Question, e.goal.Title) || !strings.Contains(q.Question, e.goal.ID) {
			t.Error(q.Question)
		}
		allow(q)
	})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	var s system.Snapshot
	if err := json.Unmarshal([]byte(r.Text), &s); err != nil {
		t.Fatal(err)
	}
	if asks != 1 || s.Status != "armed" || s.Goal != e.goal.Title || s.GoalID != e.goal.ID || s.JobID != "root" {
		t.Fatal(s, asks)
	}
	status := invoke(e.ctx, `{"action":"status"}`)
	if status.Err != nil || status.Text != r.Text {
		t.Fatal(status)
	}
	cancelled := invoke(e.ctx, `{"action":"cancel"}`)
	if cancelled.Err != nil || !strings.Contains(cancelled.Text, `"status":"cancelled"`) {
		t.Fatal(cancelled)
	}
	e.controller.Finish("root", true)
	if e.calls.Load() != 0 || len(e.timers) != 0 {
		t.Fatal("cancelled action scheduled")
	}
}

func TestPostConsentInvalidation(t *testing.T) {
	for _, action := range []string{"cancel context", "new root", "finish root", "goal changed", "goal no longer active"} {
		t.Run(action, func(t *testing.T) {
			e := env(t)
			ctx, cancel := context.WithCancel(e.ctx)
			defer cancel()
			r := consent(t, ctx, armArgs(e), func(q interactive.AskRequest) {
				switch action {
				case "cancel context":
					cancel()
				case "new root":
					e.controller.Begin("new-root")
				case "finish root":
					e.controller.Finish("root", true)
				case "goal changed":
					update(t, e, `UPDATE goals SET title = 'replacement' WHERE id = ?`, e.goal.ID)
				case "goal no longer active":
					if err := e.svc.SetStatus(context.Background(), e.goal.ID, goal.StatusPaused); err != nil {
						t.Fatal(err)
					}
				}
				allow(q)
			})
			if r.Err == nil {
				t.Fatal("stale consent armed shutdown")
			}
			if e.controller.Snapshot().Status == "armed" || e.calls.Load() != 0 {
				t.Fatal(e.controller.Snapshot())
			}
		})
	}
}

func TestGoalAndArgumentRestrictions(t *testing.T) {
	e := env(t)
	for _, tc := range []struct{ args, want string }{
		{`{"action":"arm"}`, "goal_id"},
		{`{"action":"arm","goal_id":"missing"}`, "matching ACTIVE"},
		{`{"action":"reboot"}`, "unknown host_power action"},
		{`{"action":"arm","title":"model title"}`, "unknown field"},
		{`{"action":"status"} {}`, "one JSON object"},
		{`{`, "EOF"},
	} {
		requireError(t, invoke(e.ctx, tc.args), tc.want)
	}
	other := goal.NewProjectService(e.store, "project-b")
	g, err := other.Set(context.Background(), "other project's goal", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	requireError(t, invoke(e.ctx, `{"action":"arm","goal_id":"`+g.ID+`"}`), "matching ACTIVE")
	if err := e.svc.SetStatus(context.Background(), e.goal.ID, goal.StatusPaused); err != nil {
		t.Fatal(err)
	}
	requireError(t, invoke(e.ctx, armArgs(e)), "matching ACTIVE")
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	requireError(t, invoke(ctx, `{"action":"status"}`), "context canceled")
}

func TestEligibilityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *testEnv)
		want   string
	}{
		{"incomplete", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET status = 'active' WHERE id = ?`, e.goal.ID)
		}, "not DONE"},
		{"failed", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET verification_status = 'failed' WHERE id = ?`, e.goal.ID)
		}, "PASSED"},
		{"no evidence", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET verification_evidence = '' WHERE id = ?`, e.goal.ID)
		}, "PASSED"},
		{"no timestamp", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET verified_at = NULL WHERE id = ?`, e.goal.ID)
		}, "PASSED"},
		{"skipped", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goal_tasks SET status = 'skipped' WHERE goal_id = ?`, e.goal.ID)
		}, "skipped or pending"},
		{"pending", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goal_tasks SET status = 'pending' WHERE goal_id = ?`, e.goal.ID)
		}, "skipped or pending"},
		{"stale", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET verified_at = created_at WHERE id = ?`, e.goal.ID)
		}, "newer than verification"},
		{"future", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET verified_at = ? WHERE id = ?`, time.Now().Add(time.Hour).UnixNano(), e.goal.ID)
		}, "timestamps"},
		{"wrong scope", func(t *testing.T, e *testEnv) {
			update(t, e, `UPDATE goals SET title = 'different goal' WHERE id = ?`, e.goal.ID)
		}, "definition changed"},
		{"no tasks", func(t *testing.T, e *testEnv) { update(t, e, `DELETE FROM goal_tasks WHERE goal_id = ?`, e.goal.ID) }, "no completed work"},
		{"new active goal", func(t *testing.T, e *testEnv) {
			if _, err := e.svc.Set(context.Background(), "Next goal", "", "", ""); err != nil {
				t.Fatal(err)
			}
		}, "new active goal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := env(t)
			approved := *e.goal
			complete(t, e)
			tc.mutate(t, e)
			err := eligible(context.Background(), e.svc, &approved, time.Now())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("eligibility = %v, want %q", err, tc.want)
			}
			if e.calls.Load() != 0 {
				t.Fatal("eligibility executed host power")
			}
		})
	}
}

func TestTaskMutationInvalidatesVerification(t *testing.T) {
	e := env(t)
	complete(t, e)
	// Storage permits task mutation only while the goal is active.
	if err := e.svc.SetStatus(context.Background(), e.goal.ID, goal.StatusActive); err != nil {
		t.Fatal(err)
	}
	tasks, err := e.svc.ListTasks(context.Background(), e.goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetTaskStatus(context.Background(), e.goal.ID, tasks[0].Seq, goal.TaskDone); err != nil {
		t.Fatal(err)
	}
	// Even a forged DONE state must not restore the invalidated pass.
	update(t, e, `UPDATE goals SET status = 'done', completed_at = ? WHERE id = ?`, time.Now().UnixNano(), e.goal.ID)
	err = eligible(context.Background(), e.svc, e.goal, time.Now())
	if err == nil || !strings.Contains(err.Error(), "PASSED") {
		t.Fatal(err)
	}
}

func TestTrustedLifecycleAndCompleteGoalExecuteOnceWithDetachedContext(t *testing.T) {
	e := env(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	if r := consent(t, ctx, armArgs(e), allow); r.Err != nil {
		t.Fatal(r.Err)
	}
	complete(t, e)
	if err := eligible(context.Background(), e.svc, e.goal, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Structured goal state alone must never schedule or execute host power.
	if e.controller.Snapshot().Status != "armed" || len(e.timers) != 0 || e.calls.Load() != 0 {
		t.Fatal("goal alone authorized shutdown")
	}
	cancel()
	// Only the host-owned lifecycle completion ticket supplies true here.
	e.controller.Finish("root", true)
	s := e.controller.Snapshot()
	if s.Status != "countdown" || s.Deadline != e.now.Add(time.Minute).UnixMilli() || len(e.timers) != 1 || e.calls.Load() != 0 {
		t.Fatal(s)
	}
	e.now = e.now.Add(time.Minute)
	e.timers[0]()
	if s := e.controller.Snapshot(); s.Status != "executed" || e.calls.Load() != 1 {
		t.Fatal(s, e.calls.Load())
	}
	e.timers[0]()
	e.controller.Finish("root", true)
	if e.calls.Load() != 1 {
		t.Fatal("authorization was reused")
	}
}

func TestToolCompletionRequiresBothLifecycleAndGoal(t *testing.T) {
	for _, name := range []string{"failed lifecycle", "unresolved task", "skipped task", "stale verification", "new goal"} {
		t.Run(name, func(t *testing.T) {
			e := env(t)
			if r := consent(t, e.ctx, armArgs(e), allow); r.Err != nil {
				t.Fatal(r.Err)
			}
			complete(t, e)
			successful := true
			switch name {
			case "failed lifecycle":
				// Host guards withhold the ticket despite a complete stored goal.
				successful = false
			case "unresolved task":
				update(t, e, `UPDATE goal_tasks SET status = 'pending' WHERE goal_id = ?`, e.goal.ID)
			case "skipped task":
				update(t, e, `UPDATE goal_tasks SET status = 'skipped' WHERE goal_id = ?`, e.goal.ID)
			case "stale verification":
				update(t, e, `UPDATE goals SET verified_at = created_at WHERE id = ?`, e.goal.ID)
			case "new goal":
				if _, err := e.svc.Set(context.Background(), "Next goal", "", "", ""); err != nil {
					t.Fatal(err)
				}
			}
			e.controller.Finish("root", successful)
			if s := e.controller.Snapshot(); s.Status != "blocked" || s.Error == "" {
				t.Fatal(s)
			}
			if len(e.timers) != 0 || e.calls.Load() != 0 {
				t.Fatal("ineligible work scheduled host power")
			}
		})
	}
}

func TestToolDeadlineRechecksStructuredGoalAndRootScope(t *testing.T) {
	for _, name := range []string{"unresolved task", "skipped task", "failed verification", "stale verification", "new goal", "new root"} {
		t.Run(name, func(t *testing.T) {
			e := env(t)
			if r := consent(t, e.ctx, armArgs(e), allow); r.Err != nil {
				t.Fatal(r.Err)
			}
			complete(t, e)
			e.controller.Finish("root", true)
			if s := e.controller.Snapshot(); s.Status != "countdown" || len(e.timers) != 1 {
				t.Fatal(s)
			}
			wantStatus := "blocked"
			switch name {
			case "unresolved task":
				update(t, e, `UPDATE goal_tasks SET status = 'pending' WHERE goal_id = ?`, e.goal.ID)
			case "skipped task":
				update(t, e, `UPDATE goal_tasks SET status = 'skipped' WHERE goal_id = ?`, e.goal.ID)
			case "failed verification":
				update(t, e, `UPDATE goals SET verification_status = 'failed' WHERE id = ?`, e.goal.ID)
			case "stale verification":
				update(t, e, `UPDATE goals SET verified_at = created_at WHERE id = ?`, e.goal.ID)
			case "new goal":
				if _, err := e.svc.Set(context.Background(), "Next goal", "", "", ""); err != nil {
					t.Fatal(err)
				}
			case "new root":
				e.controller.Begin("next-root")
				wantStatus = "cancelled"
			}
			e.now = e.now.Add(time.Minute)
			// Deliver even a cancelled timer to exercise stale-callback rejection.
			e.timers[0]()
			e.timers[0]()
			if s := e.controller.Snapshot(); s.Status != wantStatus || s.Deadline != 0 || e.calls.Load() != 0 {
				t.Fatal(s, e.calls.Load())
			}
		})
	}
}
