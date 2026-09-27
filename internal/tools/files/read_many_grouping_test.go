package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadManyGroupedRangesKeepOrderEOFAndFreshness(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte("old1\r\nold2\r\nold3\r\nold4\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadMany(root)
	raw := []byte(`{"reads":"source.txt:3-10 | source.txt:1-1 | source.txt:2-4 | source.txt:4-4 | source.txt:99-100 | source.txt:5-4"}`)
	result, err := tool.execute(context.Background(), raw)
	if err != nil || result.Err != nil {
		t.Fatalf("%+v %v", result, err)
	}
	sections := strings.Split(result.Text, "== [")
	if len(sections) != 7 {
		t.Fatalf("wrong output sections: %s", result.Text)
	}
	for _, i := range []int{1, 3, 4} {
		if !strings.Contains(sections[i], "[end of file at line 4]") {
			t.Fatalf("missing EOF for section %d: %s", i, sections[i])
		}
	}
	if strings.Contains(sections[2], "end of file") || !strings.Contains(sections[2], " 1 | old1") ||
		!strings.Contains(sections[5], "from=99 exceeds file length 4") ||
		!strings.Contains(sections[6], "invalid range 5-4") ||
		!strings.Contains(result.Text, "4 ok, 2 failed") || strings.Contains(result.Text, "\r") {
		t.Fatalf("ordering/partial failure/normalization changed: %s", result.Text)
	}
	if err := os.WriteFile(path, []byte("new1\nnew2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh, err := tool.execute(context.Background(), []byte(`{"reads":"source.txt:1-1 | source.txt:2-10"}`))
	if err != nil || fresh.Err != nil || strings.Contains(fresh.Text, "old") ||
		!strings.Contains(fresh.Text, "new1") || !strings.Contains(fresh.Text, "new2") ||
		!strings.Contains(fresh.Text, "[end of file at line 2]") {
		t.Fatalf("stale batch: %+v %v", fresh, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopped, err := tool.execute(ctx, []byte(`{"reads":"source.txt:1-1 | source.txt:2-10"}`))
	if err != nil || strings.Count(stopped.Text, "context canceled") != 2 || !strings.Contains(stopped.Text, "0 ok, 2 failed") {
		t.Fatalf("cancellation lost: %+v %v", stopped, err)
	}
}

func TestReadManyGroupingDoesNotHideMissingOrBinaryFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte("plain\x00binary\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"binary.bin", "missing.txt"} {
		raw, _ := json.Marshal(map[string]string{"reads": name + ":1-1 | " + name + ":2-4"})
		result, err := NewReadMany(root).execute(context.Background(), raw)
		if err != nil || strings.Count(result.Text, "error:") != 2 || !strings.Contains(result.Text, "0 ok, 2 failed") {
			t.Fatalf("group errors hidden: %+v %v", result, err)
		}
	}
}
