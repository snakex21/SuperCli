package webgui

import (
	"path/filepath"
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

func TestDueScheduleOnlyEnqueuesForManualRun(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.Local)
	var gotWorkspace, gotPrompt string
	calls := 0
	manager := &scheduleManager{
		path: filepath.Join(t.TempDir(), "schedules.json"),
		enqueue: func(workspace, prompt string) error {
			calls++
			gotWorkspace, gotPrompt = workspace, prompt
			return nil
		},
		items: []webSchedule{{
			ID: "schedule-test", Cron: "* * * * *", Prompt: "Sprawdź projekt",
			Workspace: t.TempDir(), Enabled: true,
			NextRunAt: now.Add(-time.Minute).Format(time.RFC3339),
		}},
	}

	manager.tick(now)
	if calls != 1 {
		t.Fatalf("enqueue calls = %d, want 1", calls)
	}
	if gotWorkspace != manager.items[0].Workspace {
		t.Fatalf("workspace = %q, want %q", gotWorkspace, manager.items[0].Workspace)
	}
	if gotPrompt != scheduleQueuePrefix+"Sprawdź projekt" {
		t.Fatalf("queued prompt = %q", gotPrompt)
	}
	if manager.items[0].LastRunAt == "" || manager.items[0].NextRunAt == "" {
		t.Fatalf("schedule was not advanced: %+v", manager.items[0])
	}
}

func TestScheduleManagerPersists(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	manager := newScheduleManager(dataDir, nil)
	item, err := manager.Create("0 9 * * *", "Przygotuj raport", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" || item.NextRunAt == "" {
		t.Fatalf("incomplete schedule: %+v", item)
	}
	manager.Close()

	reloaded := newScheduleManager(dataDir, nil)
	defer reloaded.Close()
	items := reloaded.List(workspace)
	if len(items) != 1 || items[0].ID != item.ID || items[0].Prompt != "Przygotuj raport" || items[0].NextRunAt == "" {
		t.Fatalf("reloaded schedules = %+v", items)
	}
}
