package dates

import (
	"testing"
	"time"
)

func TestDueDate(t *testing.T) {
	issued := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
	if got := DueDate(issued, 14); !got.Equal(issued.AddDate(0, 0, 14)) {
		t.Fatalf("weekday due date moved: %v", got)
	}
	if got := DueDate(issued, 5); got.Weekday() != time.Monday { // Saturday -> Monday
		t.Fatalf("Saturday not moved to Monday: %v", got)
	}
}
