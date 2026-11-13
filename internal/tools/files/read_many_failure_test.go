package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestReadManyAllFailedIsAnErrorWithEveryDiagnostic(t *testing.T) {
	result, err := NewReadMany(t.TempDir()).execute(context.Background(), []byte(`{"reads":"missing_a.go:1-20 | missing_b.go:1-20"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Err == nil {
		t.Fatal("all-failed batch reported success")
	}
	if !errors.Is(result.Err, os.ErrNotExist) {
		t.Fatal("underlying missing-file cause lost")
	}
	visible := core.NewOutputStore().ModelContent("read_many", result)
	for _, want := range []string{"error:", "missing_a.go:1-20", "missing_b.go:1-20", "[read_many: 0 ok, 2 failed]"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("missing diagnostic %q: %s", want, visible)
		}
	}
	if strings.Count(visible, "[read_many:") != 1 {
		t.Fatal("batch diagnostics duplicated")
	}
}

func TestReadManyFailedPreviewPreservesAllItemsAndErrorCauses(t *testing.T) {
	outcomes := make([]readManyOutcome, maxReadManyRequests)
	for i := range outcomes {
		cause := os.ErrNotExist
		if i == 5 {
			cause = context.Canceled
		}
		outcomes[i] = readManyOutcome{request: readManyRequest{File: strings.Repeat("目录", 130) + fmt.Sprintf("/file%d.go", i), From: 1, To: 300}, err: fmt.Errorf("diagnostic-%d %s: %w", i, strings.Repeat("details ", 70)+fmt.Sprintf("RECOVER_MIDDLE_%d ", i)+strings.Repeat("details ", 70), cause)}
	}
	result := renderReadMany(outcomes)
	if !errors.Is(result.Err, context.Canceled) || !errors.Is(result.Err, os.ErrNotExist) {
		t.Fatal("batch causes were flattened")
	}
	store := core.NewOutputStore()
	visible := store.ModelContent("read_many", result)
	if len(visible) > core.ModelOutputPreviewBytes+220 || !utf8.ValidString(visible) {
		t.Fatalf("invalid failure preview: bytes=%d", len(visible))
	}
	for i := range outcomes {
		if !strings.Contains(visible, fmt.Sprintf("/file%d.go:1-300", i)) || !strings.Contains(visible, fmt.Sprintf("diagnostic-%d ", i)) {
			t.Fatalf("lost failed item %d", i)
		}
	}
	if core.StoredOutputHandle(visible) == "" {
		t.Fatal("full diagnostics not retained")
	}
	if result.ModelPreview != "" {
		t.Fatal("failure still advertises a successful model preview")
	}
	raw, _ := json.Marshal(map[string]any{"handle": core.StoredOutputHandle(visible), "query": "RECOVER_MIDDLE_5"})
	recovered, err := store.ReadOutputTool().Fn(context.Background(), raw)
	if err != nil || recovered.Err != nil || !strings.Contains(recovered.Text, "RECOVER_MIDDLE_5") {
		t.Fatal("full failed-item detail was lost")
	}
}

func TestReadManyCancellationAndPartialSuccessRemainDistinct(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.go"), []byte("package ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []byte(`{"reads":"ok.go:1-10 | missing.go:1-10"}`)
	result, err := NewReadMany(dir).execute(context.Background(), args)
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "1 ok, 1 failed") || !strings.Contains(result.Text, "package ok") {
		t.Fatal("partial evidence changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, _ = NewReadMany(dir).execute(ctx, args)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatal("cancelled batch appeared successful")
	}
}
