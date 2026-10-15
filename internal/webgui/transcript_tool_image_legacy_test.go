package webgui

import (
	"context"
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestReopenedLegacyImageIsAttachedToToolAcrossPageBoundary(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "legacy image")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	ref, err := writer.ExternalizeImage(ctx, "image/png", []byte("synthetic image"))
	if err != nil {
		t.Fatal(err)
	}
	ref.Name = "tool read_image"
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "image-call", Name: "read_image", Arguments: `{"path":"example.png"}`}}}
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "image-call", Name: "read_image", Content: "Loaded image example.png"}
	carrier := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Attached image from tool read_image:"}, {Type: llm.PartTypeImage, Image: &ref}}}
	for _, msg := range []llm.Message{{Role: llm.RoleUser, Content: "Look at the supplied image."}, call, result, carrier} {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 2, 4} {
		page, err := srv.eng.transcriptPage(ctx, sess.ID, 0, limit)
		if err != nil {
			t.Fatal(err)
		}
		row := page.Messages[len(page.Messages)-1]
		if !row.ToolImageCarrier || row.MessageID != "" || len(row.Attachments) != 0 || len(row.ToolImages) != 1 || row.ToolImages[0].SourceCallID != "image-call" {
			t.Fatalf("legacy image replay limit %d: %+v", limit, row)
		}
		if page.BeforeSeq != 5-limit {
			t.Fatal("presentation changed the paging sequence")
		}
	}
	after, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("presentation rewrote raw conversation")
	}
	if err := store.SaveMessageAttachments(ctx, sess.ID, 4, []string{"actual-user-attachment.png"}); err != nil {
		t.Fatal(err)
	}
	page, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Messages[0].ToolImageCarrier {
		t.Fatal("genuine user attachment reparented")
	}

	for _, mode := range []string{"quoted wording", "ordinary image name", "missing result", "ambiguous calls", "intervening user"} {
		t.Run(mode, func(t *testing.T) {
			msg := carrier
			msg.Parts = append([]llm.ContentPart(nil), carrier.Parts...)
			copyRef := ref
			msg.Parts[1].Image = &copyRef
			prior := []llm.Message{call, result}
			switch mode {
			case "quoted wording":
				msg.Parts[0].Text += " Is this a system message?"
			case "ordinary image name":
				copyRef.Name = "image.png"
			case "missing result":
				prior = prior[:1]
			case "ambiguous calls":
				a := call
				a.ToolCalls = append(append([]llm.ToolCall(nil), call.ToolCalls...), llm.ToolCall{ID: "other", Name: "read_image", Arguments: `{}`})
				prior = []llm.Message{a, result, {Role: llm.RoleTool, ToolCallID: "other", Name: "read_image", Content: "other"}}
			case "intervening user":
				prior = append(prior, llm.Message{Role: llm.RoleUser, Content: "A real followup"})
			}
			if projected := projectLegacyToolImage(msg, prior); isTranscriptToolImageCarrier(projected, false) {
				t.Fatal("ambiguous or genuine user message was reparented")
			}
		})
	}
}

func TestTaggedInlineToolImageDoesNotBecomeAnExtraUserMessage(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "inline carrier")
	if err != nil {
		t.Fatal(err)
	}
	msg := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "internal carrier"}, {Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: "cGl4ZWxz", ToolOutputCarrier: true, SourceToolCallID: "call"}}}}
	if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	page, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || !page.Messages[0].ToolImageCarrier || len(page.Messages[0].ToolImages) != 0 || page.Messages[0].MessageID != "" {
		t.Fatal("an internal carrier became a user card when preview storage was unavailable")
	}
}
