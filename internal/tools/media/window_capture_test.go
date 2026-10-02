package media

import (
	"strings"
	"testing"
)

func TestWindowSelectionDisambiguatesBeforeCapturing(t *testing.T) {
	windows := []WindowInfo{
		{HWND: "0x10", Title: "Editor", PID: 1},
		{HWND: "0x20", Title: "notes - Editor", PID: 2},
		{HWND: "0x30", Title: "docs - Editor", PID: 3},
	}
	for _, selector := range []WindowSelector{{Title: "editor"}, {Title: " NOTES "}, {HWND: "16"}} {
		selected, err := selectCaptureWindow(windows, selector)
		if err != nil {
			t.Fatal(err)
		}
		want := "0x10"
		if selector.Title == " NOTES " {
			want = "0x20"
		}
		if selected.HWND != want {
			t.Fatalf("selector=%+v selected=%+v", selector, selected)
		}
	}
	windows = append(windows, WindowInfo{HWND: "0x40", Title: "EDITOR", PID: 4})
	_, err := selectCaptureWindow(windows, WindowSelector{Title: "editor"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous (2 matches)") || !strings.Contains(err.Error(), "0x40") {
		t.Fatal(err)
	}
	for _, selector := range []WindowSelector{{}, {Title: "Editor", HWND: "0x10"}, {HWND: "0"}, {HWND: "-1"}, {HWND: "oops"}, {Title: "missing"}, {HWND: "100"}} {
		if _, err := selectCaptureWindow(windows, selector); err == nil {
			t.Fatalf("accepted invalid or missing selector %+v", selector)
		}
	}
}

func TestWindowSelectionLimitsAmbiguityOutput(t *testing.T) {
	var windows []WindowInfo
	for i := 0; i < 32; i++ {
		windows = append(windows, WindowInfo{HWND: "0x1", Title: "Editor " + strings.Repeat("name", 100)})
	}
	_, err := selectCaptureWindow(windows, WindowSelector{Title: "Editor"})
	if err == nil || !strings.Contains(err.Error(), "32 matches") || len(err.Error()) > 1200 {
		t.Fatalf("unbounded ambiguity: %v", err)
	}
}

func TestWindowSelectionIsRestrictedToProcess(t *testing.T) {
	windows := []WindowInfo{
		{HWND: "0x10", Title: "Editor", PID: 1},
		{HWND: "0x20", Title: "Editor", PID: 2},
		{HWND: "0x30", Title: "Other window", PID: 3},
	}
	for _, selector := range []WindowSelector{{PID: 2}, {PID: 2, Title: "editor"}, {PID: 2, HWND: "0x20"}} {
		selected, err := selectCaptureWindow(windows, selector)
		if err != nil || selected.PID != 2 || selected.HWND != "0x20" {
			t.Fatalf("selector=%+v selected=%+v err=%v", selector, selected, err)
		}
	}
	for _, selector := range []WindowSelector{{PID: 99}, {PID: 2, Title: "Other"}, {PID: 2, HWND: "0x10"}} {
		if _, err := selectCaptureWindow(windows, selector); err == nil {
			t.Fatalf("selected another process for %+v", selector)
		}
	}
	windows = append(windows, WindowInfo{HWND: "0x40", Title: "Settings", PID: 2})
	if _, err := selectCaptureWindow(windows, WindowSelector{PID: 2}); err == nil || !strings.Contains(err.Error(), "2 matches") || strings.Contains(err.Error(), "0x10") {
		t.Fatalf("process ambiguity=%v", err)
	}
}
