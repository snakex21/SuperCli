package memorytools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func TestRememberExplicitCorrectionAndValidation(t *testing.T) {
	ctx := context.Background()
	project, global := openMemStore(t), openMemStore(t)
	for _, s := range []*memory.Store{project, global} {
		if err := s.Put(memory.Entry{ID: "old", Scope: memory.ScopeFact, Content: "Original shared note."}); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRememberDual(project, global)
	now := time.Now().Add(time.Second)
	r.Now = func() time.Time { return now }
	args, _ := json.Marshal(rememberArgs{Text: "Corrected setting.", Scope: "project", Supersedes: []string{"old"}})
	result, err := r.Spec().Fn(ctx, args)
	if err != nil || result.Err != nil {
		t.Fatalf("remember correction: %v %v", err, result.Err)
	}
	id := fmt.Sprintf("mem-%x", now.UnixNano())
	entry, err := project.Get(id)
	if err != nil || len(entry.Tags) != 1 || entry.Tags[0] != "supersedes:old" {
		t.Fatalf("relation not persisted: %+v %v", entry, err)
	}
	if _, err := global.Get(id); err == nil {
		t.Fatal("project correction crossed stores")
	}
	if _, err := project.Get("old"); err != nil {
		t.Fatal("archive lost")
	}
	for _, ids := range [][]string{{"bad,id"}, {""}, {"bad\nID"}, make([]string, 17)} {
		args, _ = json.Marshal(rememberArgs{Text: "Invalid replacement", Supersedes: ids})
		result, err = r.Spec().Fn(ctx, args)
		if err != nil || result.Err == nil {
			t.Fatalf("invalid IDs accepted: %v", ids)
		}
	}
	entries, err := project.List("", 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("invalid input wrote notes: %v %v", entries, err)
	}
	if !strings.Contains(r.Spec().Description, "supersedes") || !json.Valid([]byte(r.Spec().Schema)) {
		t.Fatal("correction protocol unavailable")
	}
}
