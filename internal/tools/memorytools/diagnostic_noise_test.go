package memorytools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"supercli/internal/storage/memory"
	"testing"
)

func TestRecallFallbackKeepsFactsInsteadOfUnrelatedErrorPatterns(t *testing.T) {
	s := openMemStore(t)
	if err := s.Put(memory.Entry{ID: "fact", Scope: memory.ScopeFact, Content: "Projekt uruchamia system z USB.", Source: memory.SourceAgent}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("pattern:%d", i)
		if err := s.Put(memory.Entry{ID: id, Scope: id, Content: "read_lines: no heuristic matched; defaulting to model", Source: memory.SourceAgent}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := NewRecall(s).Spec().Fn(context.Background(), json.RawMessage("{\"query\":\"architecture overview\",\"limit\":5}"))
	if err != nil || res.Err != nil || !strings.Contains(res.Text, "USB") || strings.Contains(res.Text, "heuristic") {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = NewRecall(s).Spec().Fn(context.Background(), json.RawMessage("{\"query\":\"heuristic\"}"))
	if err != nil || strings.Contains(res.Text, "defaulting to model") {
		t.Fatalf("%+v %v", res, err)
	}
}
