package session

import (
	"context"
	"supercli/internal/llm"
	"testing"
)

func TestQueuedTasksPersistAndMove(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	sess, err := s.Create("/work", "m", "x")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.EnqueueTask(ctx, "/work", sess.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnqueueTask(ctx, "/work", sess.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MoveQueuedTask(ctx, "/work", b.ID, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListQueuedTasks(ctx, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != b.ID {
		t.Fatalf("queue=%+v", got)
	}
	if err := s.UpdateQueuedTask(ctx, "/work", b.ID, "  edited second  "); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListQueuedTasks(ctx, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Prompt != "edited second" || got[0].ID != b.ID {
		t.Fatalf("edited queue=%+v", got)
	}
	if err := s.DeleteQueuedTask(ctx, "/work", a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateQueuedTaskRejectsEmptyAndForeignRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	item, err := s.EnqueueTask(ctx, "/work", "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateQueuedTask(ctx, "/work", item.ID, "  "); err == nil {
		t.Fatal("empty prompt should be rejected")
	}
	if err := s.UpdateQueuedTask(ctx, "/other", item.ID, "changed"); err == nil {
		t.Fatal("task from another workspace should not be updated")
	}
	got, err := s.ListQueuedTasks(ctx, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Prompt != "first" {
		t.Fatalf("queue=%+v", got)
	}
}

func TestReassignCwdMovesSessionsAndAppendsQueuedTasks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	oldCwd, newCwd := "/usb-old/project", "/usb-new/project"
	sess, err := s.Create(oldCwd, "m", "moved")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueTask(ctx, newCwd, "", "already there"); err != nil {
		t.Fatal(err)
	}
	moved, err := s.EnqueueTask(ctx, oldCwd, sess.ID, "move me")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReassignCwd(oldCwd, newCwd); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sess.ID)
	if err != nil || got.Cwd != newCwd {
		t.Fatalf("session after reassignment = %+v, err=%v", got, err)
	}
	queue, err := s.ListQueuedTasks(ctx, newCwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 || queue[1].ID != moved.ID || queue[1].Position != 2 {
		t.Fatalf("merged queue = %+v", queue)
	}
}

func TestForkCopiesTranscriptWithoutUsage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	parent, err := s.Create("/work", "old", "parent")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []llm.Message{{Role: llm.RoleUser, Content: "one"}, {Role: llm.RoleAssistant, Content: "two"}, {Role: llm.RoleUser, Content: "three"}} {
		enc, err := FromMessage(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AppendMessage(ctx, parent.ID, enc); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateUsage(parent.ID, 100, 20); err != nil {
		t.Fatal(err)
	}
	child, err := s.Fork(ctx, parent.ID, 2, "cloud", "new", "high")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != parent.ID || child.MessageCount != 2 || child.TokenIn != 0 {
		t.Fatalf("child=%+v", child)
	}
	msgs, err := s.ReadMessages(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[1].Content != "two" {
		t.Fatalf("messages=%+v", msgs)
	}
}

func TestForkNegativeSequenceCreatesEmptyBranch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	parent, err := s.Create("/work", "old", "parent")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := FromMessage(llm.Message{Role: llm.RoleUser, Content: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, parent.ID, enc); err != nil {
		t.Fatal(err)
	}
	child, err := s.Fork(ctx, parent.ID, -1, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if child.MessageCount != 0 {
		t.Fatalf("empty branch message count = %d", child.MessageCount)
	}
	msgs, err := s.ReadMessages(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("empty branch messages = %+v", msgs)
	}
}
