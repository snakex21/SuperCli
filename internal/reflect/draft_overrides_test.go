package reflect

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/agent"
)

func TestJSONLDraftOverrideSink_WritesLine(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONLDraftOverrideSink(dir)
	err := s.RecordDraftOverride(context.Background(), agent.DraftOverride{
		Step:          0,
		DraftModel:    "draft-mini",
		VerifierModel: "sonnet",
		DraftText:     "1. do A",
		VerifierText:  "do A, B, C, D, E",
		UserPrompt:    "ship F11",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "draft_overrides.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Trim trailing newline so json.Decode (not Decoder) works.
	trimmed := bytes.TrimRight(data, "\n")
	var rec DraftOverrideRecord
	if err := json.Unmarshal(trimmed, &rec); err != nil {
		t.Fatalf("decode: %v (raw: %q)", err, trimmed)
	}
	if rec.Step != 0 {
		t.Errorf("Step = %d, want 0", rec.Step)
	}
	if rec.DraftModel != "draft-mini" {
		t.Errorf("DraftModel = %q", rec.DraftModel)
	}
	if rec.VerifierModel != "sonnet" {
		t.Errorf("VerifierModel = %q", rec.VerifierModel)
	}
	if rec.DraftText != "1. do A" {
		t.Errorf("DraftText = %q", rec.DraftText)
	}
	if rec.VerifierText == "" {
		t.Error("VerifierText should be preserved")
	}
	if rec.UserPrompt != "ship F11" {
		t.Errorf("UserPrompt = %q", rec.UserPrompt)
	}
	if rec.TS.IsZero() {
		t.Error("TS should be set by the sink when caller didn't")
	}
}

func TestJSONLDraftOverrideSink_AppendsMultiple(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONLDraftOverrideSink(dir)
	for i := 0; i < 3; i++ {
		if err := s.RecordDraftOverride(context.Background(), agent.DraftOverride{
			Step:       i,
			DraftModel: "d",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "draft_overrides.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("got %d lines, want 3 (raw: %q)", len(lines), data)
	}
}

func TestJSONLDraftOverrideSink_CreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "subdir")
	s := NewJSONLDraftOverrideSink(dir)
	if err := s.RecordDraftOverride(context.Background(), agent.DraftOverride{Step: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft_overrides.jsonl")); err != nil {
		t.Errorf("file should exist: %v", err)
	}
}

func TestJSONLDraftOverrideSink_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONLDraftOverrideSink(dir)
	if err := s.Close(); err != nil {
		t.Errorf("close on unopened: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close on nil file: %v", err)
	}
}

func TestNoopDraftOverrideSink_DropsEverything(t *testing.T) {
	s := NewNoopDraftOverrideSink()
	if err := s.RecordDraftOverride(context.Background(), agent.DraftOverride{Step: 1}); err != nil {
		t.Errorf("noop should never error: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("noop close: %v", err)
	}
}

func TestJSONLDraftOverrideSink_SatisfiesAgentInterface(t *testing.T) {
	// Compile-time check: the concrete type satisfies
	// agent.DraftOverrideSink. The check is a no-op
	// at runtime; the type system is what matters.
	var _ agent.DraftOverrideSink = (*JSONLDraftOverrideSink)(nil)
	var _ agent.DraftOverrideSink = NoopDraftOverrideSink{}
}
