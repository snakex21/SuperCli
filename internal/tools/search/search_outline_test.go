package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestBroadSearchOverviewKeepsOwnersAndSavedEvidence(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		writeSearchFixture(t, root, fmt.Sprintf("file%d.go", i), strings.Repeat("needle "+strings.Repeat("x", 160)+"\n", 4))
	}
	tool := NewSearchCode(root)
	result, err := tool.run(context.Background(), json.RawMessage(`{"query":"needle","context":0,"max":48}`))
	if err != nil || result.Err != nil || !strings.Contains(result.ModelPreview, "Broad search overview") {
		t.Fatalf("missing overview: %v %+v", err, result)
	}
	if len(result.ModelPreview) > core.ModelOutputPreviewBytes || len(result.ModelPreview) >= len(result.Text) {
		t.Fatal("overview exceeded budget")
	}
	store := core.NewOutputStore()
	model := store.ModelContent("search_code", result)
	handle := core.StoredOutputHandle(model)
	if handle == "" {
		t.Fatal("omitted evidence is not retrievable")
	}
	if !strings.Contains(result.Text, "file9.go") || !strings.Contains(model, "selected matching lines") {
		t.Fatal("missing owner or partial-result disclosure")
	}
	raw, _ := json.Marshal(map[string]any{"handle": handle, "offset": 0, "limit": 8192})
	chunk, err := store.ReadOutputTool().Fn(context.Background(), raw)
	if err != nil || chunk.Err != nil || !strings.Contains(chunk.Text, result.Text[:8192]) {
		t.Fatalf("original evidence was not recoverable: %v %v", err, chunk.Err)
	}
	t.Logf("broad search: %d -> %d model bytes (full output retained)", len(result.Text), len(model))
}
