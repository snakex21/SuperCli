package linecount

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestAnswer is the objective verification for the eval task: the
// agent must read the (large) data.txt, count the lines containing
// ERROR and write exactly that integer to answer.txt. Nothing else is
// allowed to change.
func TestAnswer(t *testing.T) {
	data, err := os.ReadFile("data.txt")
	if err != nil {
		t.Fatalf("read data.txt: %v", err)
	}
	want := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "ERROR") {
			want++
		}
	}
	raw, err := os.ReadFile("answer.txt")
	if err != nil {
		t.Fatalf("read answer.txt: %v (did the agent write it?)", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("answer.txt is not an integer: %q", string(raw))
	}
	if got != want {
		t.Fatalf("answer.txt = %d, want %d", got, want)
	}
}
