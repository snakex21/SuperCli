package session

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestContextUsageStorageProjectionAndLegacy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sess, err := s.Create(dir, "fixture", "metadata")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, sess.ID)
	m := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "private human ą🙂"}, {Source: llm.SourceRuntime, Text: "private addon"}})
	if err = w.AppendMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	assistant := llm.Message{Role: llm.RoleAssistant, Content: "fixture answer", Origin: &llm.ContextOrigin{Source: llm.SourceModel}}
	if err = w.AppendMessage(ctx, assistant); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveContextProjection(ctx, sess.ID, []llm.Message{m, assistant}); err != nil {
		t.Fatal(err)
	}
	u := llm.MeasureContext([]llm.Message{m, assistant}, nil)
	if err = s.AppendTurnSummary(ctx, TurnSummary{SessionID: sess.ID, ToolDiag: TurnToolDiag{Context: &u}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	view, err := s.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(llm.MeasureContext(view, nil), u) {
		t.Fatal("resume projection lost source boundaries")
	}
	rows, err := s.ReadTurnSummaries(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ToolDiag.Context == nil || !reflect.DeepEqual(*rows[0].ToolDiag.Context, u) {
		t.Fatal("turn JSON lost counters")
	}
	raw, _ := json.Marshal(rows[0].ToolDiag)
	if strings.Contains(string(raw), "private") {
		t.Fatal("telemetry copied conversation")
	}
	legacy := Encoded{Role: "user", Content: "[memory_briefing] plain legacy human text"}
	old, err := legacy.ToMessage()
	if err != nil {
		t.Fatal(err)
	}
	if llm.MeasureContext([]llm.Message{old}, nil).KnownBytes != 0 {
		t.Fatal("guessed legacy provenance")
	}
}
