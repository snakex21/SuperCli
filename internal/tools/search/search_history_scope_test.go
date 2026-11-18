package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// With persistent memory off the tool is pinned to the current conversation:
// its own transcript stays searchable and readable (compaction recovery),
// other sessions are neither searched nor readable.
func TestSearchHistoryScopeCurrentSessionOnly(t *testing.T) {
	s := openHistoryStore(t)
	current, other := seedHistory(t, s)
	restrict := true
	tool := NewSearchHistory(s)
	tool.Scope = func(context.Context) (string, bool) { return current.ID, restrict }
	spec := tool.Spec()
	ctx := context.Background()

	own, _ := spec.Fn(ctx, json.RawMessage(`{"query":"refaktoryzacji"}`))
	if own.Err != nil || !strings.Contains(own.Text, current.ID) {
		t.Fatalf("current session not searchable: %v %q", own.Err, own.Text)
	}
	foreign, _ := spec.Fn(ctx, json.RawMessage(`{"query":"Krakowie"}`))
	if strings.Contains(foreign.Text, other.ID) || strings.Contains(foreign.Text, "Krakowie") {
		t.Fatalf("other session leaked through search: %q", foreign.Text)
	}
	byID, _ := spec.Fn(ctx, json.RawMessage(fmt.Sprintf(`{"query":"Krakowie","session_id":%q}`, other.ID)))
	if !strings.Contains(byID.Text, "switched off") {
		t.Fatalf("explicit other session_id must be refused, got %q", byID.Text)
	}
	read, _ := spec.Fn(ctx, json.RawMessage(fmt.Sprintf(`{"session_id":%q,"seq":1}`, other.ID)))
	if !strings.Contains(read.Text, "switched off") || strings.Contains(read.Text, "Krakowie") {
		t.Fatalf("reading another session must be refused, got %q", read.Text)
	}
	ownRead, _ := spec.Fn(ctx, json.RawMessage(fmt.Sprintf(`{"session_id":%q,"seq":1}`, current.ID)))
	if ownRead.Err != nil || !strings.Contains(ownRead.Text, "konspekt") {
		t.Fatalf("current session read failed: %v %q", ownRead.Err, ownRead.Text)
	}

	// Memory back on: unrestricted, as before.
	restrict = false
	all, _ := spec.Fn(ctx, json.RawMessage(`{"query":"Krakowie"}`))
	if !strings.Contains(all.Text, other.ID) {
		t.Fatalf("unrestricted search lost other sessions: %q", all.Text)
	}
}

func TestSearchHistoryCurrentOnlyDescription(t *testing.T) {
	plain := NewSearchHistory(nil).Spec().Description
	tool := NewSearchHistory(nil)
	tool.CurrentOnly = true
	restricted := tool.Spec().Description
	if !strings.Contains(plain, "all prior sessions") {
		t.Fatal("memory-on description changed")
	}
	if strings.Contains(restricted, "all prior sessions") || !strings.Contains(restricted, "CURRENT conversation only") {
		t.Fatalf("restricted description = %q", restricted)
	}
}
