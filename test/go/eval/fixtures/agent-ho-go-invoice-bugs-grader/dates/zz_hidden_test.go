package dates

import (
	"testing"
	"time"
)

func TestHiddenSunday(t *testing.T) {
	issued := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
	if got, want := DueDate(issued, 6), time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Sunday due date = %v, want %v", got, want)
	}
	if got, want := DueDate(issued, 5), time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Saturday due date = %v, want %v", got, want)
	}
}
