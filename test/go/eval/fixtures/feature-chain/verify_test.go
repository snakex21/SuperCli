package chain

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestAnswer verifies the multi-step task: the agent reads the integer
// in puzzle.txt, computes step 1 (n*7), step 2 (digit sum of the
// product), step 3 (previous result * 3) and writes the final integer
// to answer.txt.
func TestAnswer(t *testing.T) {
	raw, err := os.ReadFile("puzzle.txt")
	if err != nil {
		t.Fatalf("read puzzle.txt: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("puzzle.txt is not an integer: %q", string(raw))
	}
	product := n * 7
	digitsum := 0
	for _, ch := range strconv.Itoa(product) {
		digitsum += int(ch - '0')
	}
	want := digitsum * 3

	gotRaw, err := os.ReadFile("answer.txt")
	if err != nil {
		t.Fatalf("read answer.txt: %v (did the agent write it?)", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(gotRaw)))
	if err != nil {
		t.Fatalf("answer.txt is not an integer: %q", string(gotRaw))
	}
	if got != want {
		t.Fatalf("answer.txt = %d, want %d (n=%d, product=%d, digitsum=%d)", got, want, n, product, digitsum)
	}
}

var _ = strings.TrimSpace // keep import when build flags change
