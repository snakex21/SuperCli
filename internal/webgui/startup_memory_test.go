package webgui

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

func TestFirstRequestAfterRestartRetainsProjectHistory(t *testing.T) {
	ctx := context.Background()
	home, dataDir := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Create(home, "echo-test", "Parser repair")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, old.ID)
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "Fix the parser regression in parser.go."},
		{Role: llm.RoleAssistant, Content: "Parser repair completed: preserve escaped delimiters. Parser tests passed."},
	} {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	_, project := eng.webMemoryStores(home)
	if err := project.Put(memory.Entry{ID: "parser-contract", Scope: memory.ScopeFact, Content: "Project parser must retain Unicode identifiers.", Source: memory.SourceAgent}); err != nil {
		t.Fatal(err)
	}
	eng.saveWebSessionCapsule(ctx, old.ID)
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	eng, err = NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	capture := &prefixCaptureProvider{}
	eng.prov = capture
	if err := eng.runStream(ctx, "Continue the parser repair in parser.go using our prior findings.", "", "", func(wireEvent) {}); err != nil {
		t.Fatal(err)
	}
	if len(capture.requests) != 1 {
		t.Fatalf("startup model calls=%d, want 1", len(capture.requests))
	}
	var request strings.Builder
	for _, msg := range capture.requests[0] {
		request.WriteString(msg.TextOnly().Content)
		request.WriteByte(10)
	}
	for _, want := range []string{"Unicode identifiers", "preserve escaped delimiters", "Parser tests passed"} {
		if !strings.Contains(request.String(), want) {
			t.Errorf("first request lost %q", want)
		}
	}
	store, err = eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.ReadMessages(ctx, old.ID)
	if err != nil || len(prior) != 2 {
		t.Fatalf("archive modified: messages=%d err=%v", len(prior), err)
	}
}
