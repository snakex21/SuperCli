package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestCompactSummaryOnlyPersistsModelContext(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, err := session.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess, err := store.Create(t.TempDir(), "echo", "summary provenance")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	loop := makeLoopWithMessages(
		llm.Message{Role: llm.RoleSystem, Content: "current system prompt"},
		llm.Message{Role: llm.RoleUser, Content: "original question"},
		llm.Message{Role: llm.RoleAssistant, Content: "original answer"},
		llm.Message{Role: llm.RoleUser, Content: "task still in progress"},
	)
	loop.writer = writer
	for _, msg := range loop.Messages[1:] {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	summary := WrapCompactSummary("Goal: finish the current task. Done: initial inspection.")
	if removed := loop.CompactPrefixWithSummary(summary, 3); removed != 2 {
		t.Fatalf("removed=%d", removed)
	}
	rows, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[2].Content != "task still in progress" {
		t.Fatalf("summary leaked into user transcript: %+v", rows)
	}
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "task completed"}); err != nil {
		t.Fatal(err)
	}
	// Reopen to exercise durable projections and appended tails in both resume paths.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = session.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Message{
		{Role: llm.RoleUser, Content: summary},
		{Role: llm.RoleUser, Content: "task still in progress"},
		{Role: llm.RoleAssistant, Content: "task completed"},
	}
	if !reflect.DeepEqual(projected, want) {
		t.Fatalf("model lost compacted context or duplicated tail: %+v", projected)
	}
	rows, err = store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("transcript rows=%d, want 4 real messages", len(rows))
	}
	var msgs []llm.Message
	var seqs []int
	for _, row := range rows {
		msg, err := row.ToMessage()
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, msg)
		seqs = append(seqs, row.Seq)
	}
	reused, err := store.ModelContextFromTranscript(ctx, sess.ID, msgs, seqs)
	if err != nil || !reflect.DeepEqual(reused, want) {
		t.Fatalf("UI resume context=%+v, err=%v", reused, err)
	}
}

func TestLegacyCompactionSummaryRequiresCompleteGeneratedEnvelope(t *testing.T) {
	wrapped := WrapCompactSummary("Goal: preserve context.")
	cases := []struct {
		name string
		msg  llm.Message
		want bool
	}{
		{"legacy summary", llm.Message{Role: llm.RoleUser, Content: wrapped}, true},
		{"ordinary summary request", llm.Message{Role: llm.RoleUser, Content: "Please summarize our previous conversation."}, false},
		{"preamble quoted in question", llm.Message{Role: llm.RoleUser, Content: compactSummaryPreamble + "Why does this appear?"}, false},
		{"complete envelope quoted in prose", llm.Message{Role: llm.RoleUser, Content: "I found this text:\n" + wrapped}, false},
		{"assistant", llm.Message{Role: llm.RoleAssistant, Content: wrapped}, false},
		{"named user", llm.Message{Role: llm.RoleUser, Name: "human", Content: wrapped}, false},
		{"multimodal user", llm.Message{Role: llm.RoleUser, Content: wrapped, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: wrapped}}}, false},
		{"empty summary", llm.Message{Role: llm.RoleUser, Content: compactSummaryPreamble + compactSummaryEpilogue}, false},
		{"extra trailing text", llm.Message{Role: llm.RoleUser, Content: wrapped + "\nWhat do you think?"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLegacyCompactionSummary(tc.msg); got != tc.want {
				t.Fatalf("classification=%v want %v for %q", got, tc.want, strings.TrimSpace(tc.msg.Content))
			}
		})
	}
}
