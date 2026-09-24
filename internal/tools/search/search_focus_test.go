package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchFocusFindsSourceBeyondDocumentationLimit(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "a-notes.md", strings.Repeat("ResolveWidget is mentioned here\n", 80))
	writeSearchFixture(t, dir, "src/widget.go", "package widget\nfunc ResolveWidget() string { return \"target\" }\n")
	tool := NewSearchCode(dir)
	broad, err := tool.run(context.Background(), json.RawMessage(`{"query":"ResolveWidget","max":20}`))
	if err != nil || broad.Err != nil || !strings.Contains(broad.Text, "limit reached") {
		t.Fatalf("limit is invisible: %+v %v", broad, err)
	}
	focused, err := tool.run(context.Background(), json.RawMessage(`{"query":"ResolveWidget","max":20,"include":"*.go","context":1}`))
	if err != nil || focused.Err != nil || !strings.Contains(focused.Text, "target") || strings.Contains(focused.Text, "mentioned here") {
		t.Fatalf("source crowded out: %+v %v", focused, err)
	}
}
