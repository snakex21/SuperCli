package r10

import (
	"testing"
	"time"
)

func TestHiddenReview(t *testing.T) {
	fri := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	if got := NextBusinessDay(fri); got.Weekday() != time.Monday || got.Day() != 16 {
		t.Fatal("NextBusinessDay", got)
	}
	if got := NextBusinessDay(fri.AddDate(0, 0, 1)); got.Day() != 16 {
		t.Fatal("from Saturday", got)
	}
}
