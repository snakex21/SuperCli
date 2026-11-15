package snap

import (
	"testing"
	"time"
)

func TestParseName(t *testing.T) {
	at := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	s, ok := Parse(Name(at))
	if !ok || !s.Taken.Equal(at) {
		t.Fatalf("%v %v", s, ok)
	}
	if _, ok := Parse("notes.txt"); ok {
		t.Fatal("notes.txt is not a snapshot")
	}
}
