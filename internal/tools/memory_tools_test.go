package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/memory"
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
