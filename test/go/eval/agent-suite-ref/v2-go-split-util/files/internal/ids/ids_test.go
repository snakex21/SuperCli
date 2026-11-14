package ids

import (
	"strings"
	"testing"
)

func TestIDs(t *testing.T) {
	if id := New("req"); !strings.HasPrefix(id, "req_") {
		t.Fatal(id)
	}
}
