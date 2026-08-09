package facts

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestAnswer verifies the fact-merging task. Each facts_*.txt file
// contains exactly three lines of the form:
//
//	FACT <id> <value>
//
// among a lot of filler lines. The agent must collect all nine facts,
// merge them into the string "V1;V2;V3;V4;V5;V6;V7;V8;V9" where Vi is
// the value of fact with id i (ids 1..9, in order), and write exactly
// that string to answer.txt. The files are large on purpose: the
// full content cannot be kept in a single inline tool result, so the
// agent has to read in chunks and remember facts across turns.
func TestAnswer(t *testing.T) {
	values := make([]string, 10) // ids are 1..9
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("facts_%c.txt", 'a'+i-1)
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			var id int
			var val string
			if _, err := fmt.Sscanf(line, "FACT %d %s", &id, &val); err == nil && id >= 1 && id <= 9 {
				values[id] = val
			}
		}
	}
	var want []string
	for id := 1; id <= 9; id++ {
		if values[id] == "" {
			t.Fatalf("fact %d not found in any file", id)
		}
		want = append(want, values[id])
	}
	raw, err := os.ReadFile("answer.txt")
	if err != nil {
		t.Fatalf("read answer.txt: %v (did the agent write it?)", err)
	}
	got := strings.TrimSpace(string(raw))
	if got != strings.Join(want, ";") {
		t.Fatalf("answer.txt = %q, want %q", got, strings.Join(want, ";"))
	}
}
