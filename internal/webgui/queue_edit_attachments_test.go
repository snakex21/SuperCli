package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestQueueEditAttachmentsAtomicAndOptional(t *testing.T) {
	srv := newTestServer(t, false)
	a := stageQueueAttachment(t, srv, "before.png")
	b := stageQueueAttachment(t, srv, "after.png")
	task, err := srv.eng.enqueueTaskWithAttachments(context.Background(), "", "before", []string{a})
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body map[string]any, want int) {
		t.Helper()
		body["id"] = task.ID
		raw, _ := json.Marshal(body)
		rec := queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", string(raw))
		if rec.Code != want {
			t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
		}
	}
	patch(map[string]any{"prompt": "after", "attachments": []string{b}}, 200)
	rows := listQueuedOverHTTP(t, srv)
	if rows[0].Prompt != "after" || !reflect.DeepEqual(rows[0].Attachments, []string{b}) {
		t.Fatalf("saved: %+v", rows)
	}
	// Omitted attachment fields preserve the list; [] explicitly clears it.
	patch(map[string]any{"prompt": "text only edit"}, 200)
	if got := listQueuedOverHTTP(t, srv)[0].Attachments; !reflect.DeepEqual(got, []string{b}) {
		t.Fatal(got)
	}
	patch(map[string]any{"attachments": []string{}}, 200)
	if len(listQueuedOverHTTP(t, srv)[0].Attachments) != 0 {
		t.Fatal("clear not persisted")
	}
	// Attachment-only messages are valid, but an entirely empty message is not.
	patch(map[string]any{"prompt": "", "attachments": []string{a}}, 200)
	patch(map[string]any{"attachments": []string{}}, 400)
	rows = listQueuedOverHTTP(t, srv)
	if rows[0].Prompt != "" || !reflect.DeepEqual(rows[0].Attachments, []string{a}) {
		t.Fatalf("failed edit was not atomic: %+v", rows)
	}
}

func TestQueueEditAttachmentsRejectsInvalidListsWithoutChangingText(t *testing.T) {
	srv := newTestServer(t, false)
	task, err := srv.eng.enqueueTask(context.Background(), "", "original")
	if err != nil {
		t.Fatal(err)
	}
	image := stageQueueAttachment(t, srv, "valid.png")
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = image
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, paths := range map[string][]string{"count": nine, "missing": {filepath.Join(srv.eng.Home(), "missing.png")}, "outside": {outside}} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"id": task.ID, "prompt": "must not save", "attachments": paths})
			rec := queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", string(raw))
			if rec.Code != 400 {
				t.Fatalf("invalid attachment accepted: %d %s", rec.Code, rec.Body.String())
			}
			rows := listQueuedOverHTTP(t, srv)
			if rows[0].Prompt != "original" || len(rows[0].Attachments) != 0 {
				t.Fatal(rows)
			}
		})
	}
	rec := queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", `{"id":"gone","prompt":"must not resurrect","attachments":[]}`)
	if rec.Code != 404 {
		t.Fatalf("missing row: %d", rec.Code)
	}
}

func TestQueueEditAttachmentsCountedWhenMergingAndReeditingBatches(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.mu.Lock()
	srv.eng.cfg.Provider = "ollama"
	srv.eng.mu.Unlock()
	task, err := srv.eng.enqueueTask(context.Background(), "", "message")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for i := 0; i < 8; i++ {
		paths = append(paths, stageQueueAttachment(t, srv, fmt.Sprintf("image-%d.png", i)))
	}
	edit := func(id string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "attachments": paths})
		rec := queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", string(raw))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
	merge := func() {
		t.Helper()
		rows := listQueuedOverHTTP(t, srv)
		ids := []string{}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		raw, _ := json.Marshal(map[string]any{"ids": ids, "label": "Message {index}/{count}"})
		rec := queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks?merge=1", string(raw))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
	edit(task.ID)
	merge()
	rows := listQueuedOverHTTP(t, srv)
	if len(rows) != 2 || len(rows[0].Attachments) != 5 || len(rows[1].Attachments) != 3 {
		t.Fatalf("edited attachments not split: %+v", rows)
	}
	paths = nil
	for i := 0; i < 8; i++ {
		paths = append(paths, stageQueueAttachment(t, srv, fmt.Sprintf("replacement-%d.png", i)))
	}
	edit(rows[0].ID)
	rows = listQueuedOverHTTP(t, srv)
	if rows[0].Merged {
		t.Fatal("edited batch still bypasses limit checks")
	}
	merge()
	rows = listQueuedOverHTTP(t, srv)
	total := 0
	for _, row := range rows {
		if len(row.Attachments) > 5 || !row.Merged {
			t.Fatalf("unsafe batch: %+v", row)
		}
		total += len(row.Attachments)
	}
	if total != 11 {
		t.Fatalf("lost attachments: %d", total)
	}
}
