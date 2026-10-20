package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSearchHistoryReadExactHitWithoutAnotherSearch(t *testing.T) {
	s := openHistoryStore(t)
	chat, _ := seedHistory(t, s)
	tool := NewSearchHistory(s).Spec()
	ctx := context.Background()
	search, _ := tool.Fn(ctx, json.RawMessage(`{"query":"refaktoryzacji"}`))
	if search.Err != nil || !strings.Contains(search.Text, "seq=1") {
		t.Fatal("fixture search failed")
	}
	args, _ := json.Marshal(map[string]any{"session_id": chat.ID, "seq": 1, "max_bytes": 12})
	read, _ := tool.Fn(ctx, args)
	if read.Err != nil || !strings.Contains(read.Text, "konspekt") || !strings.Contains(read.Text, `"offset":12`) || !strings.Contains(read.Text, "historical evidence") {
		t.Fatalf("exact read failed: %v %s", read.Err, read.Text)
	}
	args, _ = json.Marshal(map[string]any{"session_id": chat.ID, "seq": 1, "offset": 12})
	rest, _ := tool.Fn(ctx, args)
	if rest.Err != nil || !strings.Contains(rest.Text, "[end of message]") {
		t.Fatal("cursor recovery failed")
	}
	for _, bad := range []string{`{"seq":1}`, `{"session_id":"x","seq":-1}`, fmt.Sprintf(`{"session_id":%q,"seq":1,"query":"extra"}`, chat.ID)} {
		result, _ := tool.Fn(ctx, json.RawMessage(bad))
		if result.Err == nil {
			t.Fatal("ambiguous or invalid exact read accepted")
		}
	}
}
