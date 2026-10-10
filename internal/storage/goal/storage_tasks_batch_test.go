package goal

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func verifiedBatchGoal(t *testing.T, store *Storage) *Goal {
	t.Helper()
	now := time.Now()
	g := &Goal{Title: "Synthetic objective", VerificationStatus: VerificationPassed,
		VerificationEvidence: "fixture checked", VerifiedAt: &now}
	if err := store.CreateGoal(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return g
}

func assertBatchUnchanged(t *testing.T, store *Storage, before *Goal) {
	t.Helper()
	ctx := context.Background()
	tasks, err := store.ListTasks(ctx, before.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("partial tasks persisted: %+v, %v", tasks, err)
	}
	after, err := store.GetGoal(ctx, before.ID)
	if err != nil || after.VerificationStatus != before.VerificationStatus ||
		after.VerificationEvidence != before.VerificationEvidence || after.VerifiedAt == nil || !after.VerifiedAt.Equal(*before.VerifiedAt) {
		t.Fatalf("verification changed on failure: %+v, %v", after, err)
	}
}

func TestStorage_AddTasksPreservesOrderAndInvalidatesVerificationOnce(t *testing.T) {
	db, store := newTestStorage(t)
	ctx := context.Background()
	g := &Goal{Title: "Synthetic objective"}
	if err := store.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	previous, err := store.AddTask(ctx, g.ID, "Existing completed work")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, g.ID, previous.Seq, TaskDone); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVerification(ctx, g.ID, VerificationPassed, "fixture checked"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`CREATE TABLE batch_verification_updates(n INTEGER)`,
		`CREATE TRIGGER count_batch_verification AFTER UPDATE OF verification_status,verification_evidence,verified_at ON goals BEGIN INSERT INTO batch_verification_updates VALUES(1); END`,
	} {
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	titles := []string{"Inspect", "  Preserve exact spacing  ", "Inspect"}
	added, err := store.AddTasks(ctx, g.ID, titles)
	if err != nil || len(added) != len(titles) {
		t.Fatalf("added=%+v err=%v", added, err)
	}
	all, err := store.ListTasks(ctx, g.ID)
	if err != nil || len(all) != 4 || all[0].ID != previous.ID || all[0].Status != TaskDone {
		t.Fatalf("existing task changed: %+v, %v", all, err)
	}
	ids := map[string]bool{previous.ID: true}
	for i, task := range added {
		if task.Seq != i+2 || task.Title != titles[i] || task.GoalID != g.ID || task.Status != TaskPending || task.CompletedAt != nil || task.CreatedAt.IsZero() || task.ID == "" || ids[task.ID] {
			t.Fatalf("bad added task %d: %+v", i, task)
		}
		ids[task.ID] = true
		persisted := all[i+1]
		if persisted.ID != task.ID || persisted.Seq != task.Seq || persisted.Title != task.Title || persisted.Status != task.Status || !persisted.CreatedAt.Equal(task.CreatedAt) {
			t.Fatalf("returned/persisted task differ: %+v / %+v", task, persisted)
		}
	}
	after, err := store.GetGoal(ctx, g.ID)
	if err != nil || after.VerificationStatus != VerificationNone || after.VerificationEvidence != "" || after.VerifiedAt != nil {
		t.Fatalf("stale verification: %+v %v", after, err)
	}
	var updates int
	if err := db.QueryRow(`SELECT count(*) FROM batch_verification_updates`).Scan(&updates); err != nil || updates != 1 {
		t.Fatalf("verification updates=%d err=%v", updates, err)
	}
}

func TestStorage_AddTasksValidatesEveryTitleBeforeWriting(t *testing.T) {
	for _, titles := range [][]string{nil, {}, {"first", ""}, {"first", " \t\n "}, make([]string, MaxTaskBatch+1)} {
		t.Run(fmt.Sprint(len(titles), "-", len(strings.Join(titles, ""))), func(t *testing.T) {
			_, store := newTestStorage(t)
			g := verifiedBatchGoal(t, store)
			added, err := store.AddTasks(context.Background(), g.ID, titles)
			if err == nil || added != nil {
				t.Fatalf("invalid batch succeeded: %+v, %v", added, err)
			}
			assertBatchUnchanged(t, store, g)
		})
	}
	_, store := newTestStorage(t)
	g := verifiedBatchGoal(t, store)
	titles := make([]string, MaxTaskBatch)
	for i := range titles {
		titles[i] = fmt.Sprintf("Task %d", i+1)
	}
	if added, err := store.AddTasks(context.Background(), g.ID, titles); err != nil || len(added) != MaxTaskBatch {
		t.Fatalf("maximum batch refused: %+v, %v", added, err)
	}
}

func TestStorage_AddTasksConcurrentBatchesKeepConsecutiveSequences(t *testing.T) {
	db, store := newTestStorage(t)
	db.SetMaxOpenConns(2)
	g := verifiedBatchGoal(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type outcome struct {
		tasks []*Task
		err   error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for _, titles := range [][]string{{"A1", "A2", "A3"}, {"B1", "B2", "B3"}} {
		go func(titles []string) {
			<-start
			tasks, err := store.AddTasks(ctx, g.ID, titles)
			results <- outcome{tasks: tasks, err: err}
		}(titles)
	}
	close(start)
	completed := []outcome{<-results, <-results}
	for _, result := range completed {
		if result.err != nil || len(result.tasks) != 3 {
			t.Fatalf("concurrent batch failed: %+v %v", result.tasks, result.err)
		}
		first := result.tasks[0].Seq
		if first != 1 && first != 4 {
			t.Fatalf("nonconsecutive batch start: %+v", result.tasks)
		}
		for i, task := range result.tasks {
			if task.Seq != first+i {
				t.Fatalf("interleaved batch: %+v", result.tasks)
			}
		}
	}
	tasks, err := store.ListTasks(context.Background(), g.ID)
	if err != nil || len(tasks) != 6 {
		t.Fatalf("concurrent rows=%+v err=%v", tasks, err)
	}
	for i, task := range tasks {
		prefix := strings.TrimSuffix(tasks[(i/3)*3].Title, "1")
		if task.Seq != i+1 || task.Title != fmt.Sprintf("%s%d", prefix, i%3+1) {
			t.Fatalf("batch ordering changed: %+v", tasks)
		}
	}
}

func TestStorage_AddTasksRejectsInactiveUnknownAndCanceled(t *testing.T) {
	for _, status := range []Status{StatusPaused, StatusDone, StatusAbandoned} {
		t.Run(string(status), func(t *testing.T) {
			_, store := newTestStorage(t)
			g := verifiedBatchGoal(t, store)
			if err := store.UpdateGoalStatus(context.Background(), g.ID, status); err != nil {
				t.Fatal(err)
			}
			added, err := store.AddTasks(context.Background(), g.ID, []string{"first", "second"})
			if err == nil || added != nil {
				t.Fatalf("inactive goal accepted: %+v %v", added, err)
			}
			assertBatchUnchanged(t, store, g)
		})
	}
	_, store := newTestStorage(t)
	if added, err := store.AddTasks(context.Background(), "missing", []string{"first"}); !errors.Is(err, ErrNotFound) || added != nil {
		t.Fatalf("unknown goal: %+v %v", added, err)
	}
	g := verifiedBatchGoal(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if added, err := store.AddTasks(ctx, g.ID, []string{"first", "second"}); !errors.Is(err, context.Canceled) || added != nil {
		t.Fatalf("canceled batch: %+v %v", added, err)
	}
	assertBatchUnchanged(t, store, g)
}

func TestStorage_AddTasksSQLFailuresRollbackTasksAndVerification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup []string
		stage string
	}{
		{"second insert", []string{`CREATE TRIGGER reject_batch BEFORE INSERT ON goal_tasks WHEN NEW.title='second' BEGIN SELECT RAISE(ABORT,'fixture insert failure'); END`}, "insert"},
		{"invalidation", []string{`CREATE TRIGGER reject_batch BEFORE UPDATE OF verification_status ON goals WHEN NEW.verification_status='' BEGIN SELECT RAISE(ABORT,'fixture invalidation failure'); END`}, "invalidate verification"},
		{"commit", []string{`CREATE TABLE batch_commit_guard(ref TEXT REFERENCES goals(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER reject_batch AFTER INSERT ON goal_tasks BEGIN INSERT INTO batch_commit_guard VALUES('missing'); END`}, "commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, store := newTestStorage(t)
			db.SetMaxOpenConns(1) // Inspect the same connection after a failed COMMIT.
			g := verifiedBatchGoal(t, store)
			for _, sql := range tc.setup {
				if _, err := db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			added, err := store.AddTasks(context.Background(), g.ID, []string{"first", "second", "third"})
			if err == nil || added != nil || !strings.Contains(err.Error(), tc.stage) {
				t.Fatalf("failed stage %q: %+v %v", tc.stage, added, err)
			}
			assertBatchUnchanged(t, store, g)
			if _, err := db.Exec(`DROP TRIGGER reject_batch`); err != nil {
				t.Fatal(err)
			}
			added, err = store.AddTasks(context.Background(), g.ID, []string{"repaired"})
			if err != nil || len(added) != 1 || added[0].Seq != 1 {
				t.Fatalf("rollback leaked transaction/sequence: %+v %v", added, err)
			}
		})
	}
}

var batchCancelFunctionID atomic.Uint64

func TestStorage_AddTasksCancellationAfterInsertRollsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	function := fmt.Sprintf("goal_batch_cancel_%d", batchCancelFunctionID.Add(1))
	var reached atomic.Bool
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		reached.Store(true)
		cancel()
		return int64(0), nil
	}); err != nil {
		t.Fatal(err)
	}
	db, store := newTestStorage(t)
	db.SetMaxOpenConns(1)
	g := verifiedBatchGoal(t, store)
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER cancel_batch AFTER INSERT ON goal_tasks WHEN NEW.seq=1 BEGIN SELECT %s(); END`, function)); err != nil {
		t.Fatal(err)
	}
	added, err := store.AddTasks(ctx, g.ID, []string{"first", "second", "third"})
	if !errors.Is(err, context.Canceled) || added != nil || !reached.Load() {
		t.Fatalf("did not cancel a live insert: %+v %v reached=%t", added, err, reached.Load())
	}
	assertBatchUnchanged(t, store, g)
	if _, err := db.Exec(`DROP TRIGGER cancel_batch`); err != nil {
		t.Fatal(err)
	}
	if added, err := store.AddTasks(context.Background(), g.ID, []string{"repaired"}); err != nil || len(added) != 1 || added[0].Seq != 1 {
		t.Fatalf("canceled transaction was pooled: %+v %v", added, err)
	}
}

func TestStorage_AddTasksRechecksProjectScopeBeforeWriting(t *testing.T) {
	_, store := newTestStorage(t)
	g := verifiedBatchGoal(t, store) // Legacy/unassigned; not visible to a scoped service.
	if added, err := store.addTasks(context.Background(), g.ID, []string{"first", "second"}, "project-a"); !errors.Is(err, ErrNotFound) || added != nil {
		t.Fatalf("unassigned scope accepted: %+v %v", added, err)
	}
	assertBatchUnchanged(t, store, g)
	if _, err := store.db.Exec(`UPDATE goals SET project_key='project-b' WHERE id=?`, g.ID); err != nil {
		t.Fatal(err)
	}
	if added, err := store.addTasks(context.Background(), g.ID, []string{"first", "second"}, "project-a"); !errors.Is(err, ErrNotFound) || added != nil {
		t.Fatalf("changed scope accepted: %+v %v", added, err)
	}
	assertBatchUnchanged(t, store, g)
	if _, err := store.db.Exec(`UPDATE goals SET project_key=? WHERE id=?`, GlobalProjectKey, g.ID); err != nil {
		t.Fatal(err)
	}
	if added, err := store.addTasks(context.Background(), g.ID, []string{"first", "second"}, "project-a"); err != nil || len(added) != 2 {
		t.Fatalf("explicit global scope refused: %+v %v", added, err)
	}
}

func TestStorage_AddTaskKeepsSingleTitleContract(t *testing.T) {
	_, store := newTestStorage(t)
	g := verifiedBatchGoal(t, store)
	for _, title := range []string{"  exact single title  ", " "} {
		task, err := store.AddTask(context.Background(), g.ID, title)
		if err != nil || task.Title != title {
			t.Fatalf("legacy single title changed: %+v %v", task, err)
		}
	}
	tasks, err := store.ListTasks(context.Background(), g.ID)
	if err != nil || len(tasks) != 2 || !reflect.DeepEqual([]string{tasks[0].Title, tasks[1].Title}, []string{"  exact single title  ", " "}) {
		t.Fatalf("single titles changed: %+v %v", tasks, err)
	}
}
