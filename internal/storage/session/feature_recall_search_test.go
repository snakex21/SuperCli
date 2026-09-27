package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestSearchSessionMatchesScopeRankAndBounds(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	add := func(cwd string, texts ...string) string {
		t.Helper()
		sess, err := store.Create(cwd, "echo-test", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		writer := NewWriter(store, sess.ID)
		for _, text := range texts {
			if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: text}); err != nil {
				t.Fatal(err)
			}
		}
		return sess.ID
	}
	cwd := "/workspace/it's-project"
	best := add(cwd, "needle with many unrelated words decreasing the relevance of this older message", "needle", "needle")
	second := add(cwd, "needle another useful session with different past work")
	current := add(cwd, "needle")
	foreign := add("/workspace/foreign", "needle")
	// A session with no recorded workspace must stay outside a project recall.
	unknown := add("/legacy", "needle")
	if _, err := store.db.Exec(`UPDATE sessions SET cwd='' WHERE id=?`, unknown); err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchSessionMatches(ctx, "needle", []string{cwd}, current, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].SessionID != best || hits[1].SessionID != second {
		t.Fatalf("wrong scoped ranking: %+v", hits)
	}
	if hits[0].Seq != 3 || hits[0].Role != "assistant" || hits[0].CreatedAt.IsZero() || !strings.Contains(hits[0].Snippet, "<mark>needle</mark>") {
		t.Fatalf("not the latest best matching message: %+v", hits[0])
	}
	one, err := store.SearchSessionMatches(ctx, "needle", []string{cwd}, current, 1)
	if err != nil || len(one) != 1 || one[0].SessionID != best {
		t.Fatalf("limit: %+v / %v", one, err)
	}
	empty, err := store.SearchSessionMatches(ctx, "needle", nil, current, 2)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty allowlist became unrestricted: %+v / %v", empty, err)
	}
	all, err := store.SearchHistory(ctx, "needle", "", "", time.Time{}, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundForeign := false
	for _, hit := range all {
		if hit.SessionID == foreign {
			foundForeign = true
		}
	}
	if !foundForeign {
		t.Fatal("general history search unexpectedly changed scope")
	}
	workspaces, err := store.HistoryWorkspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("expected distinct recorded workspaces only: %v", workspaces)
	}
	if _, err := store.SearchSessionMatches(ctx, "", []string{cwd}, "", 2); err == nil {
		t.Fatal("empty query accepted")
	}
	if _, err := store.SearchSessionMatches(ctx, "\"", []string{cwd}, "", 2); err == nil {
		t.Fatal("invalid FTS query did not return an error")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.SearchSessionMatches(canceled, "needle", []string{cwd}, "", 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("search ignored cancellation: %v", err)
	}
	if _, err := store.HistoryWorkspaces(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("workspace lookup ignored cancellation: %v", err)
	}
}
