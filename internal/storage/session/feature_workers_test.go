package session

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

func TestWorkerRowsRoundTripAndCascade(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "m", "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	row := WorkerRow{SessionID: sess.ID, ID: "worker-3", Agent: "code", Description: "task", Status: "running", Runs: 1,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "brief"}, {Role: llm.RoleAssistant, Content: "progress"}}}
	if err := store.SaveWorker(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.Status, row.Runs = "done", 2
	if err := store.SaveWorker(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LoadWorker(ctx, sess.ID, "worker-3")
	if err != nil || !ok || got.Status != "done" || got.Runs != 2 || len(got.Messages) != 2 || got.Messages[1].Content != "progress" {
		t.Fatalf("load = %+v ok=%v err=%v", got, ok, err)
	}
	if err := store.SetWorkerStatus(ctx, sess.ID, "worker-3", "interrupted", "gone"); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListWorkers(ctx, sess.ID)
	if err != nil || len(list) != 1 || list[0].Status != "interrupted" || list[0].LastError != "gone" {
		t.Fatalf("list = %+v %v", list, err)
	}
	ids, _ := store.WorkerIDs(ctx)
	if len(ids) != 1 || ids[0] != "worker-3" {
		t.Fatalf("ids = %v", ids)
	}
	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.LoadWorker(ctx, sess.ID, "worker-3"); ok {
		t.Fatal("worker row survived its session")
	}
}

// A worker's command/check counters are saved, listed with the briefs and
// never lowered by a later save (a resumed run starts its own tool list).
func TestWorkerCommandCountersPersist(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "m", "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	row := WorkerRow{SessionID: sess.ID, ID: "worker-4", Agent: "code", Description: "task", Status: "done", Runs: 1, CommandsRun: 3, ChecksRun: 2}
	if err := store.SaveWorker(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.CommandsRun, row.ChecksRun, row.Runs = 0, 0, 2
	if err := store.SaveWorker(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LoadWorker(ctx, sess.ID, "worker-4")
	if err != nil || !ok || got.CommandsRun != 3 || got.ChecksRun != 2 {
		t.Fatalf("load = %+v ok=%v err=%v", got, ok, err)
	}
	briefs, err := store.ListWorkerBriefs(ctx, sess.ID)
	if err != nil || len(briefs) != 1 || briefs[0].CommandsRun != 3 || briefs[0].ChecksRun != 2 {
		t.Fatalf("briefs = %+v %v", briefs, err)
	}
}
