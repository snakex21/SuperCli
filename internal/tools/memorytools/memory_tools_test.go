package memorytools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/storage/memory"
)

func openMemStore(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("memory.OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRememberAndRecall_RoundTrip(t *testing.T) {
	s := openMemStore(t)
	rem := NewRemember(s)
	rec := NewRecall(s)

	res, err := rem.Spec().Fn(context.Background(),
		json.RawMessage(`{"text":"user prefers table-driven tests","topic":"preferences"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("remember: err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "remembered [mem-") {
		t.Errorf("remember text = %q", res.Text)
	}

	res, err = rec.Spec().Fn(context.Background(),
		json.RawMessage(`{"query":"tests"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("recall: err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "table-driven tests") {
		t.Errorf("recall text = %q, want the remembered fact", res.Text)
	}
	if !strings.Contains(res.Text, "preferences") {
		t.Errorf("recall text = %q, want the topic tag", res.Text)
	}
}

func TestRemember_EmptyTextFails(t *testing.T) {
	rem := NewRemember(openMemStore(t))
	res, err := rem.Spec().Fn(context.Background(), json.RawMessage(`{"text":"  "}`))
	if err != nil {
		t.Fatalf("go-level error: %v", err)
	}
	if res.Err == nil {
		t.Error("empty text should be a tool error")
	}
}

func TestRemember_RejectsTranscriptSizedMemory(t *testing.T) {
	rem := NewRemember(openMemStore(t))
	res, err := rem.Spec().Fn(context.Background(), json.RawMessage(`{"text":"`+strings.Repeat("x", maxRememberTextBytes+1)+`"}`))
	if err != nil {
		t.Fatalf("go-level error: %v", err)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "summarize") {
		t.Fatalf("want actionable size error, got %+v", res)
	}
}

func TestRecall_NoMatches(t *testing.T) {
	rec := NewRecall(openMemStore(t))
	res, err := rec.Spec().Fn(context.Background(), json.RawMessage(`{"query":"zzzznothing"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("recall: err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "no memories match") {
		t.Errorf("recall text = %q", res.Text)
	}
}

func TestRecall_CrossLanguageFallbackFindsProfileFact(t *testing.T) {
	s := openMemStore(t)
	if err := s.Put(memory.Entry{
		ID:      "name-pl",
		Scope:   memory.ScopeFact,
		Content: "Użytkownik ma na imię Maks.",
		Source:  memory.SourceAgent,
		Tags:    []string{"user_profile"},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := NewRecall(s).Spec().Fn(context.Background(), json.RawMessage(`{"query":"user name"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("recall: err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "Maks") {
		t.Fatalf("cross-language fallback did not return the profile fact: %s", res.Text)
	}
	if !strings.Contains(res.Text, "cross-language fallback") {
		t.Fatalf("fallback should be explicit in tool output: %s", res.Text)
	}
}

func TestRemember_UserProfileDefaultsToGlobalPreference(t *testing.T) {
	project := openMemStore(t)
	global := openMemStore(t)
	res, err := NewRememberDual(project, global).Spec().Fn(
		context.Background(),
		json.RawMessage(`{"text":"Użytkownik ma na imię Maks.","topic":"user_profile"}`),
	)
	if err != nil || res.Err != nil {
		t.Fatalf("remember: err=%v res.Err=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "(preference, global)") {
		t.Fatalf("profile fact should be routed to global preferences: %s", res.Text)
	}
	projectEntries, err := project.Recent("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectEntries) != 0 {
		t.Fatalf("profile fact unexpectedly stored in project memory: %+v", projectEntries)
	}
	globalEntries, err := global.Recent(memory.ScopePreference, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(globalEntries) != 1 || !strings.Contains(globalEntries[0].Content, "Maks") {
		t.Fatalf("global profile memory missing: %+v", globalEntries)
	}
}

func TestRecall_EmptyQueryFails(t *testing.T) {
	rec := NewRecall(openMemStore(t))
	res, err := rec.Spec().Fn(context.Background(), json.RawMessage(`{"query":""}`))
	if err != nil {
		t.Fatalf("go-level error: %v", err)
	}
	if res.Err == nil {
		t.Error("empty query should be a tool error")
	}
}

func TestRecall_ClampsLimitAndOutputBytes(t *testing.T) {
	s := openMemStore(t)
	for i := 0; i < 12; i++ {
		if err := s.Put(memory.Entry{
			ID: fmt.Sprintf("hit-%02d", i), Scope: memory.ScopeFact,
			Content: "needle " + strings.Repeat(fmt.Sprintf("payload-%02d ", i), 240), Source: memory.SourceAgent,
		}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewRecall(s).Spec().Fn(context.Background(), json.RawMessage(`{"query":"needle","limit":999}`))
	if err != nil || res.Err != nil {
		t.Fatalf("recall err=%v result=%v", err, res.Err)
	}
	if len(res.Text) > maxRecallOutputBytes+128 { // UTF-8-safe omission marker has small bookkeeping overhead.
		t.Fatalf("recall output=%d bytes, want bounded near %d", len(res.Text), maxRecallOutputBytes)
	}
	if strings.Count(res.Text, "- [") > maxRecallLimit {
		t.Fatalf("recall returned too many entries: %s", res.Text)
	}
	if !strings.Contains(res.Text, "omitted_bytes") {
		t.Fatalf("bounded output should disclose truncation: %s", res.Text)
	}
}

func TestMemoryTools_NotWired(t *testing.T) {
	res, _ := NewRemember(nil).Spec().Fn(context.Background(), json.RawMessage(`{"text":"x"}`))
	if !strings.Contains(res.Text, "not available") {
		t.Errorf("remember nil store text = %q", res.Text)
	}
	res, _ = NewRecall(nil).Spec().Fn(context.Background(), json.RawMessage(`{"query":"x"}`))
	if !strings.Contains(res.Text, "not available") {
		t.Errorf("recall nil store text = %q", res.Text)
	}
}
