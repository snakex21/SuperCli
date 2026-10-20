package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

const legacyWorkerNotice = "<task-notification>\n<task-id>worker-5</task-id>\n<agent>review</agent>\n<worker-model>fixture-model</worker-model>\n<reasoning-effort>high</reasoning-effort>\n<status>done</status>\n<summary>Review complete</summary>\n<result>Full report ą日本語 <details>retained</details>\nline two</result>\n</task-notification>"

func TestTranscriptWorkerNotificationClassificationIsConservative(t *testing.T) {
	for _, text := range []string{legacyWorkerNotice, strings.ReplaceAll(legacyWorkerNotice, "\n", ""), legacyWorkerNotice + "\n[Attached: worker tool observations (historical snapshots).]\n[stored tool output: handle=fixture]"} {
		if !isTranscriptWorkerNotification(llm.Message{Role: llm.RoleUser, Content: text}) {
			t.Fatalf("complete old worker report not recognized: %q", text)
		}
	}
	for _, text := range []string{
		"Co oznacza ten raport?\n" + legacyWorkerNotice,
		"```xml\n" + legacyWorkerNotice + "\n```",
		legacyWorkerNotice + "\nTo jest mój komentarz.",
		"<task-notification>worker done</task-notification>",
		strings.Replace(legacyWorkerNotice, "</task-notification>", "", 1),
		strings.Replace(legacyWorkerNotice, "worker-5", "worker-not-a-number", 1),
		strings.Replace(legacyWorkerNotice, "<status>done</status>", "<status>unknown</status>", 1),
	} {
		if isTranscriptWorkerNotification(llm.Message{Role: llm.RoleUser, Content: text}) {
			t.Fatalf("human quote or malformed envelope reclassified: %q", text)
		}
	}
	for _, message := range []llm.Message{
		{Role: llm.RoleAssistant, Content: legacyWorkerNotice},
		{Role: llm.RoleUser, Name: "human", Content: legacyWorkerNotice},
		{Role: llm.RoleUser, Content: legacyWorkerNotice, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "authored text"}}},
	} {
		if isTranscriptWorkerNotification(message) {
			t.Fatalf("ordinary authored message reclassified: %+v", message)
		}
	}
	if !isTranscriptWorkerNotification(llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "host report without XML"}) {
		t.Fatal("explicit host provenance was ignored")
	}
}

func TestTranscriptWorkerNotificationsAreFoldedToolsWithoutRewindIdentity(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "worker history")
	if err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Real request"},
		{Role: llm.RoleUser, Content: legacyWorkerNotice},
		{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: legacyWorkerNotice},
		{Role: llm.RoleUser, Content: "Co oznacza ten raport?\n" + legacyWorkerNotice},
		{Role: llm.RoleAssistant, Content: "Answer"},
		{Role: llm.RoleUser, Content: legacyWorkerNotice},
	}
	for _, message := range messages {
		if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveMessageAttachments(ctx, sess.ID, 6, []string{"human-attachment.txt"}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	full, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil || len(full) != len(messages) {
		t.Fatalf("transcript: %+v, %v", full, err)
	}
	var paged []transcriptMsg
	cursor := 0
	for {
		page, err := srv.eng.transcriptPage(ctx, sess.ID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(append([]transcriptMsg(nil), page.Messages...), paged...)
		if !page.HasMore {
			break
		}
		cursor = page.BeforeSeq
	}
	if !reflect.DeepEqual(full, paged) {
		t.Fatal("paged history differs from full history")
	}
	for i, message := range full {
		if message.Content != messages[i].Content {
			t.Fatal("report content was changed or removed")
		}
		if i == 1 || i == 2 {
			if message.Role != "tool" || message.Name != "task" || message.MessageID != "" {
				t.Fatalf("worker restored as rewindable human: %+v", message)
			}
		} else if i != 4 && (message.Role != "user" || message.MessageID == "") {
			t.Fatalf("real user lost rewind identity: %+v", message)
		}
	}
	if notice, err := isStoredWorkerNotification(ctx, store, sess.ID, 6, messages[5]); err != nil || notice {
		t.Fatalf("real attachment was not preserved as human provenance: %v", err)
	}
	after, err := store.ReadMessages(ctx, sess.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("presentation mutated canonical stored conversation")
	}
}

func TestWorkerNotificationsCannotBeRewindTargets(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprint(named), func(t *testing.T) {
			srv := newTestServer(t, false)
			sess, store := createRewindSession(t, srv, "worker rewind")
			ctx := context.Background()
			message := llm.Message{Role: llm.RoleUser, Content: legacyWorkerNotice}
			if named {
				message.Name = llm.WorkerNotificationName
			}
			if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, message); err != nil {
				t.Fatal(err)
			}
			before, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := store.ReadUserReceiptAt(ctx, sess.ID, 5)
			if err != nil {
				t.Fatal(err)
			}
			for _, files := range []bool{false, true} {
				rec := httptest.NewRecorder()
				body := fmt.Sprintf(`{"session_id":%q,"selected_seq":5,"selected_message_id":"%d","rewind_files":%t}`, sess.ID, receipt.ID, files)
				srv.handleSessionRewind(rec, httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body)))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("worker accepted for rewind: %d %s", rec.Code, rec.Body.String())
				}
			}
			rec := httptest.NewRecorder()
			url := fmt.Sprintf("/api/checkpoint/rewind?session=%s&from_seq=5&message_id=%d", sess.ID, receipt.ID)
			srv.handleCheckpointRewind(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("worker accepted for rewind preview: %d %s", rec.Code, rec.Body.String())
			}
			after, err := store.ReadMessages(ctx, sess.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected rewind modified stored history")
			}
			var dto map[string]any
			data, _ := json.Marshal(transcriptMsg{Role: "tool", Name: "task", Content: legacyWorkerNotice})
			if err := json.Unmarshal(data, &dto); err != nil || dto["message_id"] != nil {
				t.Fatal("notification serialization leaked a rewind identity")
			}
		})
	}
}
