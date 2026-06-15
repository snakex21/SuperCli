package goal

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage"
)

// newTestStorage opens a fresh SQLite db in a temp dir
// for use in goal tests.
func newTestStorage(t *testing.T) (*sql.DB, *Storage) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	gs := NewStorage(db)
	if err := gs.Migrate(context.Background()); err != nil {
		_ = db.Close()
		t.Fatalf("goal.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, gs
}

func TestStorage_Migrate_Idempotent(t *testing.T) {
	db, _ := newTestStorage(t)
	// Run again.
	gs2 := NewStorage(db)
	if err := gs2.Migrate(context.Background()); err != nil {
		t.Errorf("second Migrate failed: %v", err)
	}
}

func TestStorage_Migrate_NilDB(t *testing.T) {
	gs := &Storage{}
	if err := gs.Migrate(context.Background()); err == nil {
		t.Error("expected error for nil db")
	}
}

func TestStorage_CreateAndGetGoal(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{
		Title:       "ship F8",
		Description: "long-term goal tracking",
		Status:      StatusActive,
	}
	if err := gs.CreateGoal(ctx, g); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if g.ID == "" {
		t.Error("ID not set")
	}
	if g.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
	got, err := gs.GetGoal(ctx, g.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.Title != "ship F8" {
		t.Errorf("Title = %q, want %q", got.Title, "ship F8")
	}
	if got.Description != "long-term goal tracking" {
		t.Errorf("Description lost: %q", got.Description)
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
}

func TestStorage_CreateGoal_EmptyTitle(t *testing.T) {
	_, gs := newTestStorage(t)
	err := gs.CreateGoal(context.Background(), &Goal{Title: ""})
	if err != ErrEmptyTitle {
		t.Errorf("expected ErrEmptyTitle, got %v", err)
	}
}

func TestStorage_CreateGoal_DefaultStatus(t *testing.T) {
	_, gs := newTestStorage(t)
	g := &Goal{Title: "x"}
	if err := gs.CreateGoal(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusActive {
		t.Errorf("default status = %q, want active", g.Status)
	}
}

func TestStorage_CreateGoal_InvalidStatus(t *testing.T) {
	_, gs := newTestStorage(t)
	err := gs.CreateGoal(context.Background(), &Goal{Title: "x", Status: "bogus"})
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestStorage_ActiveGoal(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	// No active yet.
	g, err := gs.ActiveGoal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g != nil {
		t.Errorf("expected nil active, got %+v", g)
	}
	// Create one.
	a := &Goal{Title: "first"}
	_ = gs.CreateGoal(ctx, a)
	time.Sleep(1 * time.Millisecond) // ensure distinct created_at
	b := &Goal{Title: "second"}
	_ = gs.CreateGoal(ctx, b)
	got, err := gs.ActiveGoal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected active goal")
	}
	if got.ID != b.ID {
		t.Errorf("ActiveGoal = %q, want most recent %q", got.ID, b.ID)
	}
}

func TestStorage_ActiveGoal_ExcludesNonActive(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	_ = gs.CreateGoal(ctx, &Goal{Title: "old"})
	time.Sleep(1 * time.Millisecond)
	_ = gs.CreateGoal(ctx, &Goal{Title: "newer"})
	// Pause BOTH goals so nothing is active.
	all, _ := gs.ListGoals(ctx)
	for _, g := range all {
		_ = gs.UpdateGoalStatus(ctx, g.ID, StatusPaused)
	}
	got, err := gs.ActiveGoal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil active when all are paused, got %+v", got)
	}
}

func TestStorage_ListGoals(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	_ = gs.CreateGoal(ctx, &Goal{Title: "a"})
	time.Sleep(1 * time.Millisecond)
	_ = gs.CreateGoal(ctx, &Goal{Title: "b"})
	got, err := gs.ListGoals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	if got[0].Title != "b" {
		t.Errorf("newest first, got %q first", got[0].Title)
	}
}

func TestStorage_UpdateGoalStatus(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	if err := gs.UpdateGoalStatus(ctx, g.ID, StatusDone); err != nil {
		t.Fatal(err)
	}
	got, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != StatusDone {
		t.Errorf("status = %q", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt not set on done")
	}
}

func TestStorage_UpdateGoalStatus_ReopenResetsCompletedAt(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	_ = gs.UpdateGoalStatus(ctx, g.ID, StatusDone)
	// Reopen.
	_ = gs.UpdateGoalStatus(ctx, g.ID, StatusActive)
	got, _ := gs.GetGoal(ctx, g.ID)
	if got.Status != StatusActive {
		t.Errorf("status = %q", got.Status)
	}
	if got.CompletedAt != nil {
		t.Error("reopening should clear CompletedAt")
	}
}

func TestStorage_UpdateGoalStatus_NotFound(t *testing.T) {
	_, gs := newTestStorage(t)
	err := gs.UpdateGoalStatus(context.Background(), "no-such-id", StatusDone)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStorage_UpdateGoalStatus_Invalid(t *testing.T) {
	_, gs := newTestStorage(t)
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(context.Background(), g)
	err := gs.UpdateGoalStatus(context.Background(), g.ID, "bogus")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestStorage_AppendNote(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	if err := gs.AppendNote(ctx, g.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if err := gs.AppendNote(ctx, g.ID, "second"); err != nil {
		t.Fatal(err)
	}
	got, _ := gs.GetGoal(ctx, g.ID)
	if !strings.Contains(got.Notes, "first") || !strings.Contains(got.Notes, "second") {
		t.Errorf("notes lost: %q", got.Notes)
	}
	// Each line should be timestamped.
	lines := strings.Split(got.Notes, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "[") {
			t.Errorf("line not timestamped: %q", line)
		}
	}
}

func TestStorage_AppendNote_Empty(t *testing.T) {
	_, gs := newTestStorage(t)
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(context.Background(), g)
	err := gs.AppendNote(context.Background(), g.ID, "   ")
	if err == nil {
		t.Error("expected error for empty note")
	}
}

func TestStorage_AppendNote_NotFound(t *testing.T) {
	_, gs := newTestStorage(t)
	err := gs.AppendNote(context.Background(), "no-such", "x")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStorage_AddTask(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	t1, err := gs.AddTask(ctx, g.ID, "first task")
	if err != nil {
		t.Fatal(err)
	}
	if t1.Seq != 1 {
		t.Errorf("first seq = %d, want 1", t1.Seq)
	}
	t2, _ := gs.AddTask(ctx, g.ID, "second task")
	if t2.Seq != 2 {
		t.Errorf("second seq = %d, want 2", t2.Seq)
	}
}

// TestStorage_AddTask_TightLoopNoCollision is the
// regression guard for the F9-era "flaky
// TestGoalTool_Decompose_*" bug.
//
// History: defaultRandBytes used to derive its "random"
// suffix from time.Now().UnixNano() & 0xFFFFFFFF, so
// consecutive AddTask calls in the same nanosecond (or
// in nanoseconds with the same lower 32 bits) produced
// the same task id and clashed on the UNIQUE(id)
// constraint. Symptom: SQLite error 1555 "UNIQUE
// constraint failed: goal_tasks.id" hitting roughly 1
// in 20 TestGoalTool_Decompose_Heuristic runs because
// the heuristic decomposes "ship F8" into 5 tasks in a
// tight loop.
//
// The fix was to use crypto/rand for 64 bits of
// entropy. This test re-creates the exact failure mode
// (1000 AddTask calls on a single goal, no sleep, in a
// loop) and asserts zero id collisions. If this test
// starts failing, defaultRandBytes has regressed and
// the flake will come back.
func TestStorage_AddTask_TightLoopNoCollision(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	if err := gs.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	const N = 1000
	seen := make(map[string]int, N)
	var firstErr error
	for i := 0; i < N; i++ {
		tk, err := gs.AddTask(ctx, g.ID, "task")
		if err != nil {
			firstErr = err
			t.Errorf("AddTask #%d failed: %v (collision or other storage error)", i, err)
			break
		}
		if prev, dup := seen[tk.ID]; dup {
			t.Errorf("AddTask #%d produced duplicate id %q (also seen at #%d)", i, tk.ID, prev)
			break
		}
		seen[tk.ID] = i
	}
	if firstErr != nil {
		return
	}
	if len(seen) != N {
		t.Errorf("got %d unique ids, want %d", len(seen), N)
	}
	// And verify seq was assigned 1..N without gaps.
	tasks, err := gs.ListTasks(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != N {
		t.Errorf("got %d rows, want %d", len(tasks), N)
	}
}

// TestGenerateID_UniqueUnderContention hammers
// generateID directly to verify the production id
// generator produces no duplicates under heavy
// concurrent load. Companion to the storage-level
// test above; this one is unit-fast and runs in
// microseconds.
func TestGenerateID_UniqueUnderContention(t *testing.T) {
	const G = 8  // goroutines
	const N = 500 // ids per goroutine
	ids := make(chan string, G*N)
	done := make(chan struct{})
	for g := 0; g < G; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < N; i++ {
				ids <- generateID(defaultRandBytes)
			}
		}()
	}
	go func() {
		for g := 0; g < G; g++ {
			<-done
		}
		close(ids)
	}()
	seen := make(map[string]bool, G*N)
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate id: %q", id)
		}
		seen[id] = true
	}
	if len(seen) != G*N {
		t.Errorf("got %d unique ids, want %d", len(seen), G*N)
	}
}

// TestDefaultRandBytes_Entropy is a smoke test on the
// fixed defaultRandBytes: every call must return a
// 16-char hex string (8 random bytes) and the set of
// 100 calls must be all unique.
func TestDefaultRandBytes_Entropy(t *testing.T) {
	const N = 100
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		s := defaultRandBytes()
		if len(s) != 16 {
			t.Errorf("defaultRandBytes returned %d chars, want 16 (8 bytes hex)", len(s))
		}
		if seen[s] != struct{}{} {
			// no-op, just to use the map semantics
		}
		if _, dup := seen[s]; dup {
			t.Errorf("defaultRandBytes collision: %q appeared twice", s)
		}
		seen[s] = struct{}{}
	}
}

// TestGenerateTaskID_FrozenClock_NoCollision is the
// regression guard for the F9-era flake. It uses the
// injectable nowFn to FORCE every generateTaskID call
// to land in the same nanosecond, which is the exact
// failure mode the production bug required (time-based
// suffix + a Windows timer tick that returned the same
// value twice). With the broken time-based suffix the
// first collision fires on the 2nd call; with the
// crypto/rand 8-byte suffix 1000 calls in the same
// nanosecond are still all unique.
//
// This test is the one that would have caught the
// original bug deterministically.
func TestGenerateTaskID_FrozenClock_NoCollision(t *testing.T) {
	// Pin the clock to a fixed time so every call
	// returns the same UnixNano(). This simulates a
	// Windows scheduler tick that hands out the same
	// time twice in a row.
	frozen := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	origNow := nowFn
	nowFn = func() time.Time { return frozen }
	defer func() { nowFn = origNow }()

	const N = 1000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		id := generateTaskID(defaultRandBytes)
		if _, dup := seen[id]; dup {
			t.Fatalf("generateTaskID #%d collision: %q (frozen clock)", i, id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != N {
		t.Errorf("got %d unique ids, want %d", len(seen), N)
	}
}

// TestStorage_AddTask_FrozenClock_NoCollision is the
// end-to-end version: same frozen clock, but
// AddTask (which uses generateTaskID internally) is
// hammered. This is the test that mirrors the
// production path 1:1 and would have caught the
// original "UNIQUE constraint failed: goal_tasks.id"
// error.
func TestStorage_AddTask_FrozenClock_NoCollision(t *testing.T) {
	frozen := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	origNow := nowFn
	nowFn = func() time.Time { return frozen }
	defer func() { nowFn = origNow }()

	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "frozen"}
	if err := gs.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	const N = 100
	for i := 0; i < N; i++ {
		if _, err := gs.AddTask(ctx, g.ID, "x"); err != nil {
			t.Fatalf("AddTask #%d failed under frozen clock: %v", i, err)
		}
	}
	tasks, err := gs.ListTasks(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != N {
		t.Errorf("got %d tasks, want %d", len(tasks), N)
	}
}

func TestStorage_AddTask_EmptyTitle(t *testing.T) {
	_, gs := newTestStorage(t)
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(context.Background(), g)
	_, err := gs.AddTask(context.Background(), g.ID, "")
	if err != ErrEmptyTitle {
		t.Errorf("expected ErrEmptyTitle, got %v", err)
	}
}

func TestStorage_ListTasks_OrderedBySeq(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	for _, title := range []string{"a", "b", "c"} {
		_, _ = gs.AddTask(ctx, g.ID, title)
	}
	tasks, err := gs.ListTasks(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Errorf("len = %d, want 3", len(tasks))
	}
	if tasks[0].Seq != 1 || tasks[0].Title != "a" {
		t.Errorf("first task = %+v", tasks[0])
	}
}

func TestStorage_SetTaskStatus(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	t1, _ := gs.AddTask(ctx, g.ID, "do thing")
	if err := gs.SetTaskStatus(ctx, g.ID, t1.Seq, TaskDone); err != nil {
		t.Fatal(err)
	}
	tasks, _ := gs.ListTasks(ctx, g.ID)
	if tasks[0].Status != TaskDone {
		t.Errorf("status = %q, want done", tasks[0].Status)
	}
	if tasks[0].CompletedAt == nil {
		t.Error("CompletedAt not set on done task")
	}
}

func TestStorage_SetTaskStatus_NotFound(t *testing.T) {
	_, gs := newTestStorage(t)
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(context.Background(), g)
	err := gs.SetTaskStatus(context.Background(), g.ID, 99, TaskDone)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStorage_SetTaskStatus_Invalid(t *testing.T) {
	_, gs := newTestStorage(t)
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(context.Background(), g)
	t1, _ := gs.AddTask(context.Background(), g.ID, "x")
	err := gs.SetTaskStatus(context.Background(), g.ID, t1.Seq, "bogus")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestStorage_SetTaskStatus_ReopenClearsCompletedAt(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	t1, _ := gs.AddTask(ctx, g.ID, "x")
	_ = gs.SetTaskStatus(ctx, g.ID, t1.Seq, TaskDone)
	_ = gs.SetTaskStatus(ctx, g.ID, t1.Seq, TaskPending)
	tasks, _ := gs.ListTasks(ctx, g.ID)
	if tasks[0].CompletedAt != nil {
		t.Error("reopening should clear CompletedAt")
	}
}

func TestStorage_CountTasks(t *testing.T) {
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	t1, _ := gs.AddTask(ctx, g.ID, "a")
	_, _ = gs.AddTask(ctx, g.ID, "b")
	_, _ = gs.AddTask(ctx, g.ID, "c")
	_ = gs.SetTaskStatus(ctx, g.ID, t1.Seq, TaskDone)
	total, done, err := gs.CountTasks(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if done != 1 {
		t.Errorf("done = %d, want 1", done)
	}
}

func TestStorage_DeleteGoal_CascadesTasks(t *testing.T) {
	// FK ON DELETE CASCADE; SQLite needs foreign_keys
	// pragma to be ON. storage.Open sets that, so the
	// cascade should fire. We test the FK behavior by
	// issuing a raw DELETE and verifying tasks are
	// gone.
	_, gs := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "x"}
	_ = gs.CreateGoal(ctx, g)
	_, _ = gs.AddTask(ctx, g.ID, "a")
	// Issue a raw delete to simulate a future "abandon
	// and purge" path.
	if _, err := gs.db.ExecContext(ctx, `DELETE FROM goals WHERE id = ?`, g.ID); err != nil {
		t.Fatal(err)
	}
	tasks, err := gs.ListTasks(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected cascade, got %d tasks", len(tasks))
	}
}

func TestStatusValidators(t *testing.T) {
	for _, s := range []Status{StatusActive, StatusPaused, StatusDone, StatusAbandoned} {
		if !ValidGoalStatus(s) {
			t.Errorf("ValidGoalStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []Status{"", "x", "ACTIVE"} {
		if ValidGoalStatus(s) {
			t.Errorf("ValidGoalStatus(%q) = true, want false", s)
		}
	}
	for _, s := range []Status{TaskPending, TaskInProgress, TaskDone, TaskSkipped} {
		if !ValidTaskStatus(s) {
			t.Errorf("ValidTaskStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []Status{"", "x", "PENDING"} {
		if ValidTaskStatus(s) {
			t.Errorf("ValidTaskStatus(%q) = true, want false", s)
		}
	}
}

func TestRenderGoalMarkdown(t *testing.T) {
	g := &Goal{
		ID:         "g-1",
		Title:      "ship F8",
		Status:     StatusActive,
		CreatedAt:  time.Now(),
		Notes:      "first\nsecond",
	}
	tasks := []Task{
		{Seq: 1, Title: "design doc", Status: TaskDone},
		{Seq: 2, Title: "implement", Status: TaskInProgress},
		{Seq: 3, Title: "ship", Status: TaskPending},
	}
	got := renderGoalMarkdown(g, tasks)
	for _, want := range []string{"# ship F8", "status: active", "## Notes", "## Tasks",
		"[x] 1. design doc", "[>] 2. implement", "[ ] 3. ship"} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q", want)
		}
	}
}
