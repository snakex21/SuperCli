package session

import (
	"context"
	"reflect"
	"testing"
)

func TestQueuedTaskAttachmentsPersistAcrossEditMoveAndReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	paths := []string{"/work/.supercli/attachments/a/shot.png", "/work/doc.pdf"}
	item, err := s.EnqueueTaskWithAttachments(ctx, "/work", "", "with files", append(paths, " ", paths[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(item.Attachments, paths) {
		t.Fatalf("enqueue attachments=%v", item.Attachments)
	}
	if _, err := s.EnqueueTask(ctx, "/work", "", "plain"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateQueuedTask(ctx, "/work", item.ID, "edited"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveQueuedTask(ctx, "/work", item.ID, 1); err != nil {
		t.Fatal(err)
	}
	if used, err := s.AttachmentPathInUse(ctx, paths[0]); err != nil || !used {
		t.Fatalf("queued attachment in use=%v err=%v", used, err)
	}
	if used, err := s.AttachmentPathInUse(ctx, "/work/other.png"); err != nil || used {
		t.Fatalf("unrelated path in use=%v err=%v", used, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.ListQueuedTasks(ctx, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Prompt != "plain" || got[0].Attachments != nil || got[1].Prompt != "edited" || !reflect.DeepEqual(got[1].Attachments, paths) {
		t.Fatalf("queue after reopen=%+v", got)
	}
	if _, err := s.EnqueueTaskWithAttachments(ctx, "/work", "", " ", nil); err == nil {
		t.Fatal("empty task without attachments must be rejected")
	}
}
