package scratchpad

import (
	"context"
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
