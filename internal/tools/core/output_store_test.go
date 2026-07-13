package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutputStore_SmallResultUnchanged(t *testing.T) {
	s := NewOutputStore()
	if got := s.Compact("read_many", "small result"); got != "small result" {
		t.Fatalf("small result changed: %q", got)
	}
}

func TestOutputStore_LargeResultPreviewAndRead(t *testing.T) {
	s := NewOutputStore()
	original := "BEGIN\n" + strings.Repeat("0123456789", 2000) + "\nEND"
	preview := s.Compact("read_many", original)
	if len(preview) >= len(original)/2 {
		t.Fatalf("preview too large: %d vs %d", len(preview), len(original))
	}
	for _, want := range []string{"handle=out_000001", "BEGIN", "END", "read_output"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview missing %q", want)
		}
	}

	tool := s.ReadOutputTool()
	res, err := tool.Fn(context.Background(), json.RawMessage(`{"handle":"out_000001","offset":4096,"limit":4096}`))
	if err != nil || res.Err != nil {
		t.Fatalf("read_output: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, "bytes 4096:8192") || !strings.Contains(res.Text, "[next offset: 8192]") {
		t.Fatalf("unexpected chunk metadata:\n%s", res.Text)
	}
}

func TestOutputStore_UTF8ChunkBoundaries(t *testing.T) {
	s := NewOutputStore()
	original := strings.Repeat("ż", outputInlineBytes)
	preview := s.Compact("read_many", original)
	if !utf8.ValidString(preview) {
		t.Fatal("preview is not valid UTF-8")
	}
	chunk, start, _, err := s.read("out_000001", 1, 101)
	if err != nil {
		t.Fatal(err)
	}
	if start != 2 || !utf8.ValidString(chunk) {
		t.Fatalf("invalid aligned chunk: start=%d valid=%v", start, utf8.ValidString(chunk))
	}
}

func TestRegistryEnsureReadOutput_Idempotent(t *testing.T) {
	r := NewRegistry()
	r.EnsureReadOutput()
	r.EnsureReadOutput()
	if r.Len() != 1 {
		t.Fatalf("Len=%d, want 1", r.Len())
	}
	if !r.IsVisible("read_output") {
		t.Fatal("read_output must be visible")
	}
}

func TestOutputStore_EvictsLeastRecentlyUsed(t *testing.T) {
	s := NewOutputStore()
	for i := 0; i < outputStoreItems+1; i++ {
		if _, ok := s.put(strings.Repeat("x", 16)); !ok {
			t.Fatal("small output was not stored")
		}
	}
	if _, _, _, err := s.read("out_000001", 0, 1); err == nil {
		t.Fatal("oldest output survived the entry cap")
	}
	if got, _, _, err := s.read("out_000033", 0, 1); err != nil || got != "x" {
		t.Fatalf("newest output missing: got=%q err=%v", got, err)
	}
}
