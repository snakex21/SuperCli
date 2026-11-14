package r10

import (
	"testing"
	"time"
)

func TestVisible(t *testing.T) {
	if NextBusinessDay(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)).Day() != 11 {
		t.Fatal("nbd")
	}
}
