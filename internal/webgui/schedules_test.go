package webgui

import (
	"testing"
	"time"
)

func TestNextCronTime(t *testing.T) {
	from := time.Date(2026, time.July, 16, 10, 7, 0, 0, time.Local)
	next, err := nextCronTime("*/15 9-17 * * 1-5", from)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hour() != 10 || next.Minute() != 15 {
		t.Fatalf("next = %v, want 10:15", next)
	}
}

func TestScheduleManagerPersists(t *testing.T) {
	manager := newScheduleManager(t.TempDir(), nil)
	item, err := manager.Create("0 9 * * *", "Przygotuj raport", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" || item.NextRunAt == "" {
		t.Fatalf("incomplete schedule: %+v", item)
	}
	manager.Close()
}
