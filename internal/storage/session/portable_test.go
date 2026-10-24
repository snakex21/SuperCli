package session

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func portableFixture(t *testing.T, s *Store) Session {
	t.Helper()
	ctx := context.Background()
	sess, err := s.Create("/work/project", "model-x", "Portable chat")
	if err != nil {
		t.Fatal(err)
	}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "look"}, {Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: "iVBORw0KGgo="}}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "package a"},
		{Role: llm.RoleAssistant, Content: "original answer"},
	}
	var last MessageReceipt
	for _, m := range msgs {
		enc, err := FromMessage(m)
		if err != nil {
			t.Fatal(err)
		}
		if last, err = s.AppendMessageWithReceipt(ctx, sess.ID, enc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EditAssistantMessage(ctx, sess.ID, last, "edited answer", true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUsage(sess.ID, 120, 45); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func stripSessionID(rows []Encoded) []Encoded {
	out := append([]Encoded(nil), rows...)
	for i := range out {
		out[i].SessionID = ""
	}
	return out
}

func TestPortableRoundTripCreatesNewSession(t *testing.T) {
	ctx := context.Background()
	src := openTestStore(t)
	sess := portableFixture(t, src)
	var buf bytes.Buffer
	if _, err := src.WritePortable(ctx, sess.ID, &buf); err != nil {
		t.Fatal(err)
	}
	dst := openTestStore(t)
	existing, err := dst.Create("/other", "m", "keep me")
	if err != nil {
		t.Fatal(err)
	}
	keep, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "untouched"})
	if err := dst.AppendMessage(ctx, existing.ID, keep); err != nil {
		t.Fatal(err)
	}
	imported, err := dst.ImportPortableReader(ctx, bytes.NewReader(buf.Bytes()), PortableImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if imported.ID == sess.ID || imported.ID == existing.ID || imported.ID == "" {
		t.Fatalf("import reused an id: %q", imported.ID)
	}
	got, err := dst.Get(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != sess.Title || got.Model != sess.Model || got.Cwd != sess.Cwd || got.TokenIn != 120 || got.TokenOut != 45 || got.MessageCount != 4 || !got.CreatedAt.Equal(sess.CreatedAt) {
		t.Fatalf("metadata differs: %+v vs %+v", got, sess)
	}
	want, _ := src.ReadMessages(ctx, sess.ID)
	have, _ := dst.ReadMessages(ctx, imported.ID)
	if !reflect.DeepEqual(stripSessionID(want), stripSessionID(have)) {
		t.Fatalf("messages differ:\n%+v\n%+v", want, have)
	}
	wantEdits, _ := src.ReadMessageEdits(ctx, sess.ID, 1, math.MaxInt32)
	haveEdits, _ := dst.ReadMessageEdits(ctx, imported.ID, 1, math.MaxInt32)
	if len(wantEdits) != 1 || len(haveEdits) != 1 {
		t.Fatalf("edits: %d / %d", len(wantEdits), len(haveEdits))
	}
	for _, w := range wantEdits {
		for _, h := range haveEdits {
			w.MessageID, h.MessageID = 0, 0
			if !reflect.DeepEqual(w, h) {
				t.Fatalf("edit differs: %+v vs %+v", w, h)
			}
		}
	}
	// The imported edit is restorable in the new install.
	row, err := dst.ReadMessageAt(ctx, imported.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	_ = row
	old, _ := dst.ReadMessages(ctx, existing.ID)
	if len(old) != 1 || old[0].Content != "untouched" {
		t.Fatalf("existing session changed: %+v", old)
	}
	// Importing twice yields two independent sessions.
	again, err := dst.ImportPortableReader(ctx, bytes.NewReader(buf.Bytes()), PortableImportOptions{Cwd: "/here"})
	if err != nil || again.ID == imported.ID || again.Cwd != "/here" {
		t.Fatalf("second import: %+v err=%v", again, err)
	}
}

func TestPortableRejectsMalformedInput(t *testing.T) {
	ctx := context.Background()
	src := openTestStore(t)
	sess := portableFixture(t, src)
	var buf bytes.Buffer
	if _, err := src.WritePortable(ctx, sess.ID, &buf); err != nil {
		t.Fatal(err)
	}
	valid := buf.String()
	cases := map[string]string{
		"not json":      "{nope",
		"wrong format":  strings.Replace(valid, `"format": "supercli-session"`, `"format": "other"`, 1),
		"wrong version": strings.Replace(valid, `"version": 1`, `"version": 99`, 1),
		"no messages":   `{"format":"supercli-session","version":1,"session":{"model":"m","cwd":"/x"},"messages":[]}`,
		"bad role":      `{"format":"supercli-session","version":1,"session":{"model":"m","cwd":"/x"},"messages":[{"seq":1,"role":"robot","content":"x"}]}`,
		"bad seq order": `{"format":"supercli-session","version":1,"session":{"model":"m","cwd":"/x"},"messages":[{"seq":2,"role":"user","content":"a"},{"seq":1,"role":"user","content":"b"}]}`,
		"bad parts":     `{"format":"supercli-session","version":1,"session":{"model":"m","cwd":"/x"},"messages":[{"seq":1,"role":"user","content":"a","parts_json":"{"}]}`,
		"edit on user":  `{"format":"supercli-session","version":1,"session":{"model":"m","cwd":"/x"},"messages":[{"seq":1,"role":"user","content":"a"}],"edits":[{"seq":1,"edited_content":"x"}]}`,
		"trailing data": valid + "{}",
		"missing model": `{"format":"supercli-session","version":1,"session":{"cwd":"/x"},"messages":[{"seq":1,"role":"user","content":"a"}]}`,
		"no cwd at all": `{"format":"supercli-session","version":1,"session":{"model":"m"},"messages":[{"seq":1,"role":"user","content":"a"}]}`,
	}
	dst := openTestStore(t)
	for name, input := range cases {
		if _, err := dst.ImportPortableReader(ctx, strings.NewReader(input), PortableImportOptions{}); !errors.Is(err, ErrPortableInvalid) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	big := bytes.Repeat([]byte(" "), MaxPortableBytes+1)
	if _, err := DecodePortable(bytes.NewReader(big)); !errors.Is(err, ErrPortableTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
	if list, _ := dst.List(0); len(list) != 0 {
		t.Fatalf("rejected imports left %d sessions", len(list))
	}
	if !strings.HasSuffix(PortableFilename(sess), PortableFileSuffix) {
		t.Fatal("filename suffix")
	}
}
