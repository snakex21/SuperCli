package scratchpad

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchpadRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	write, _ := s.Spec().Fn(context.Background(), []byte(`{"action":"write","name":"worker-1","text":"found the race"}`))
	if write.Err != nil {
		t.Fatal(write.Err)
	}
	read, _ := s.Spec().Fn(context.Background(), []byte(`{"action":"read","name":"worker-1"}`))
	if read.Err != nil || !strings.Contains(read.Text, "race") {
		t.Fatalf("read=%+v", read)
	}
	list, _ := s.Spec().Fn(context.Background(), []byte(`{"action":"list"}`))
	if list.Text != "worker-1" {
		t.Fatalf("list=%q", list.Text)
	}
}

func TestScratchpadRetentionKeepsNewestBoundedSet(t *testing.T) {
	base := t.TempDir()
	s := New(base)
	for i := 0; i < maxNotes+5; i++ {
		name := fmt.Sprintf("note-%02d", i)
		res, _ := s.Spec().Fn(context.Background(), []byte(fmt.Sprintf(`{"action":"write","name":%q,"text":"x"}`, name)))
		if res.Err != nil {
			t.Fatal(res.Err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(base, ".supercli", "scratchpad"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxNotes {
		t.Fatalf("notes = %d, want %d", len(entries), maxNotes)
	}
	newest := filepath.Join(base, ".supercli", "scratchpad", fmt.Sprintf("note-%02d.md", maxNotes+4))
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("newest note was evicted: %v", err)
	}
}
