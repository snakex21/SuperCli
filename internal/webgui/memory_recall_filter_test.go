package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func TestWebRecallFiltersBeforeLimit(t *testing.T) {
	dataDir, home := t.TempDir(), t.TempDir()
	for _, global := range []bool{false, true} {
		var s *memory.Store
		var err error
		if global {
			s, err = memory.OpenStore(dataDir)
		} else {
			s, err = memory.OpenProjectStore(dataDir, home)
		}
		if err != nil {
			t.Fatal(err)
		}
		fact := fmt.Sprintf("needle portable configuration beside executable; global=%v", global)
		if err := s.Put(memory.Entry{ID: "fact", Scope: memory.ScopeFact, Content: fact, CreatedAt: time.Unix(1, 0)}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 12; i++ {
			id := fmt.Sprintf("pattern:%d", i)
			if err := s.Put(memory.Entry{ID: id, Scope: id, Content: "needle no heuristic matched"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	_, registry, err := eng.buildLoopWithSession(nil, nil, eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if registry == nil {
		t.Fatal("missing loop registry")
	}
	for _, scope := range []string{"project", "global"} {
		for _, query := range []string{"needle", "unmatchedcrosslanguage"} {
			t.Run(scope+"/"+query, func(t *testing.T) {
				args, _ := json.Marshal(map[string]any{"query": query, "scope": scope, "limit": 1})
				result, err := registry.Execute(context.Background(), "recall", args)
				if err != nil || result.Err != nil {
					t.Fatalf("recall: %v / %v", err, result.Err)
				}
				expected := fmt.Sprintf("global=%v", scope == "global")
				if !strings.Contains(result.Text, expected) || strings.Contains(result.Text, "heuristic") {
					t.Fatalf("web recall lost the saved fact: %s", result.Text)
				}
				if strings.Contains(result.Text, "cross-language fallback") != (query != "needle") {
					t.Fatalf("wrong source: %s", result.Text)
				}
			})
		}
	}
}
