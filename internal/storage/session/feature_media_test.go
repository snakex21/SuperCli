package session

import (
	"context"
	"encoding/base64"
	"os"
	"testing"

	"supercli/internal/llm"
)

func TestReadModelContextExternalizesLegacyInlineImage(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "model", "legacy-media")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("legacy-image")
	legacy := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: "look"},
		{Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(raw)}},
	}}
	if err := NewWriter(store, sess.ID).AppendMessage(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}

	msgs, err := store.ReadModelContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || len(msgs[0].Parts) != 2 || msgs[0].Parts[1].Image == nil {
		t.Fatalf("unexpected resumed context: %+v", msgs)
	}
	img := msgs[0].Parts[1].Image
	if img.Data != "" || img.Path == "" || img.ID == "" || img.Active {
		t.Fatalf("legacy image was not externalized/dormant on resume: %+v", img)
	}
	got, err := os.ReadFile(img.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("externalized legacy bytes = %q, want %q", got, raw)
	}
}

func TestWriterExternalizeImageDeduplicatesAndDeleteCleans(t *testing.T) {
	home := t.TempDir()
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	sess, err := store.Create(t.TempDir(), "model", "media")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	data := []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4}

	first, err := writer.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == "" || first.ID == "" || first.Data != "" || first.URL != "" || first.Active {
		t.Fatalf("externalized ref = %+v, want dormant id+path image ref", first)
	}
	if second.Path != first.Path || second.ID != first.ID {
		t.Fatalf("same image was not deduplicated: %+v != %+v", second, first)
	}
	got, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("stored bytes = %v, want %v", got, data)
	}

	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("session media survived Delete: %v", err)
	}
}
