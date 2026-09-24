package core

import (
	"strings"
	"testing"
)

func TestOutputHandleCannotResolveToAnotherStoresResult(t *testing.T) {
	before, after := NewOutputStore(), NewOutputStore()
	old, ok := before.put(strings.Repeat("earlier result", 1000))
	if !ok {
		t.Fatal("old output not retained")
	}
	newer, ok := after.put(strings.Repeat("different result", 1000))
	if !ok {
		t.Fatal("new output not retained")
	}
	if old == newer {
		t.Errorf("two different results share handle %q", old)
	}
	if text, _, _, err := after.read(old, 0, 100); err == nil {
		t.Fatalf("stale handle silently returned different output: %q", text)
	}
}

// Single-result fixtures must follow the actual opaque handle rather than
// assume every registry starts at the same identity.
func onlyOutputHandle(t *testing.T, s *OutputStore) string {
	t.Helper()
	if len(s.entries) != 1 {
		t.Fatalf("expected one output, got %d", len(s.entries))
	}
	for handle := range s.entries {
		return handle
	}
	t.Fatal("no handle")
	return ""
}
