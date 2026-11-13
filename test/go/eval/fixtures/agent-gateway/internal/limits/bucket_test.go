package limits

import (
	"testing"
	"time"
)

func TestBurstThenDeny(t *testing.T) {
	b := NewBuckets(60, 2)
	fixed := time.Unix(0, 0)
	b.now = func() time.Time { return fixed }
	if !b.Allow("k") || !b.Allow("k") || b.Allow("k") {
		t.Fatal("expected two allowed then deny")
	}
}
