package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A search that runs out of its time limit ends with a clear message on how
// to narrow it, instead of running until the worker's run limit.
func TestSearchCodeStopsAtItsTimeLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 400; i++ {
		sub := filepath.Join(dir, "build-migration", fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte(strings.Repeat("nothing here\n", 200)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saved := searchCodeTimeout
	searchCodeTimeout = time.Nanosecond
	defer func() { searchCodeTimeout = saved }()
	res, err := NewSearchCode(dir).Spec().Fn(context.Background(), json.RawMessage(`{"query":"needle","path":"build-migration"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "search_code stopped after") || !strings.Contains(res.Err.Error(), "build-migration is too large") {
		t.Fatalf("time limit not reported: %+v", res)
	}
	searchCodeTimeout = time.Minute
	res, _ = NewSearchCode(dir).Spec().Fn(context.Background(), json.RawMessage(`{"query":"needle","path":"build-migration"}`))
	if res.Err != nil {
		t.Fatalf("a search within the limit failed: %v", res.Err)
	}
}
