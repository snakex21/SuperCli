package webgui

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestTranscriptToolImageCarrierRequiresExplicitHostMetadata(t *testing.T) {
	path := "/portable/session-media/" + strings.Repeat("a", 64) + ".png"
	image := llm.ImageRef{Path: path, MediaType: "image/png", SourceToolCallID: "call-exact", ToolOutputCarrier: true}
	carrier := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Arbitrary host wording, not matched"}, {Type: llm.PartTypeImage, Image: &image}}}
	before, _ := json.Marshal(carrier)
	want := []transcriptToolImage{{SourceCallID: "call-exact", Path: "session:fixture/" + strings.Repeat("a", 64) + ".png"}}
	if got := transcriptToolImagePreviews("fixture", carrier, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("host projection: %+v", got)
	}
	for _, mode := range []string{"ordinary", "missing id", "unmarked", "mixed image", "real text", "real attachment", "assistant", "named user", "nontext part", "unsafe path", "empty carrier"} {
		t.Run(mode, func(t *testing.T) {
			mixed := carrier
			mixed.Parts = append([]llm.ContentPart(nil), carrier.Parts...)
			ref := image
			mixed.Parts[1].Image = &ref
			attachments := false
			switch mode {
			case "ordinary":
				ref.SourceToolCallID = ""
				ref.ToolOutputCarrier = false
			case "missing id":
				ref.SourceToolCallID = ""
			case "unmarked":
				ref.ToolOutputCarrier = false
			case "mixed image":
				mixed.Parts = append(mixed.Parts, llm.ContentPart{Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Path: path}})
			case "real text":
				mixed.Content = "Real authored message"
			case "real attachment":
				attachments = true
			case "assistant":
				mixed.Role = llm.RoleAssistant
			case "named user":
				mixed.Name = "user"
			case "nontext part":
				mixed.Parts = append(mixed.Parts, llm.ContentPart{Type: llm.PartTypeReasoning})
			case "unsafe path":
				ref.Path = "https://untrusted.invalid/image.svg"
			case "empty carrier":
				mixed.Parts = mixed.Parts[:1]
			}
			if got := transcriptToolImagePreviews("fixture", mixed, attachments); len(got) != 0 {
				t.Fatalf("mixed/unmarked message marked carrier: %+v", got)
			}
		})
	}
	after, _ := json.Marshal(carrier)
	if string(before) != string(after) {
		t.Fatal("projection mutated stored content")
	}
}

func TestTranscriptToolImageProvenancePagingAndLegacyFallback(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "tool images")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	ref, err := writer.ExternalizeImage(ctx, "image/png", []byte("synthetic pixels"))
	if err != nil {
		t.Fatal(err)
	}
	ref.SourceToolCallID, ref.ToolOutputCarrier = "reused-call", true
	carrier := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Unchanged host text"}, {Type: llm.PartTypeImage, Image: &ref}}}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "reused-call", Name: "read_image", Arguments: "{}"}}}
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "reused-call", Name: "read_image", Content: "exact raw tool output"}
	for _, msg := range []llm.Message{{Role: llm.RoleUser, Content: "question"}, call, result, carrier, {Role: llm.RoleAssistant, Content: "answer"}, call, result, carrier} {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	token := sessionImagePreviewPath(sess.ID, ref.Path)
	full, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil || len(full) != 8 {
		t.Fatalf("full transcript: %+v %v", full, err)
	}
	for _, idx := range []int{3, 7} {
		row := full[idx]
		if row.Seq != idx+1 || row.Role != "user" || row.Content != "Unchanged host text" || !row.ToolImageCarrier || len(row.Attachments) != 0 || !reflect.DeepEqual(row.ToolImages, []transcriptToolImage{{SourceCallID: "reused-call", Path: token}}) {
			t.Fatalf("origin/cursor/text: %+v", row)
		}
	}
	last, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 1)
	if err != nil || !last.HasMore || last.BeforeSeq != 8 || len(last.Messages) != 1 || !last.Messages[0].ToolImageCarrier {
		t.Fatalf("leading image page: %+v %v", last, err)
	}
	older, err := srv.eng.transcriptPage(ctx, sess.ID, last.BeforeSeq, 1)
	if err != nil || len(older.Messages) != 1 || older.Messages[0].Seq != 7 || older.Messages[0].ToolCallID != "reused-call" || older.Messages[0].Content != result.Content {
		t.Fatalf("origin on earlier page changed: %+v %v", older, err)
	}
	rowsBefore, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.eng.transcript(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	rowsAfter, err := store.ReadMessages(ctx, sess.ID)
	if err != nil || !reflect.DeepEqual(rowsBefore, rowsAfter) {
		t.Fatal("projection rewrote history")
	}
	// Genuine attachment metadata wins over any synthetic-looking image refs.
	if err := store.SaveMessageAttachments(ctx, sess.ID, 8, []string{"real-user-file.png"}); err != nil {
		t.Fatal(err)
	}
	last, err = srv.eng.transcriptPage(ctx, sess.ID, 0, 1)
	if err != nil || last.Messages[0].ToolImageCarrier || len(last.Messages[0].ToolImages) != 0 || !reflect.DeepEqual(last.Messages[0].Attachments, []string{"real-user-file.png"}) {
		t.Fatalf("real attachment lost: %+v %v", last, err)
	}
	legacyRef := ref
	legacyRef.SourceToolCallID, legacyRef.ToolOutputCarrier = "", false
	legacy := carrier
	legacy.Parts = append([]llm.ContentPart(nil), carrier.Parts...)
	legacy.Parts[1].Image = &legacyRef
	if err := writer.AppendMessage(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	last, err = srv.eng.transcriptPage(ctx, sess.ID, 0, 1)
	if err != nil || last.Messages[0].ToolImageCarrier || len(last.Messages[0].ToolImages) != 0 || !reflect.DeepEqual(last.Messages[0].Attachments, []string{token}) {
		t.Fatalf("legacy compatibility changed: %+v %v", last, err)
	}
	// An ordinary text message adds no presentation-only JSON fields.
	raw, _ := json.Marshal(full[0])
	if strings.Contains(string(raw), "tool_images") || strings.Contains(string(raw), "tool_image_carrier") {
		t.Fatalf("ordinary DTO gained fields: %s", raw)
	}
}
