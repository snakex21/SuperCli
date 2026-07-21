package webgui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowSizeIsStoredPerDataDirectory(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if err := saveWindowSize(first, 1180, 740); err != nil {
		t.Fatal(err)
	}
	if err := saveWindowSize(second, 920, 620); err != nil {
		t.Fatal(err)
	}
	if width, height := loadWindowSize(first, 1440, 900, 1900, 1000); width != 1180 || height != 740 {
		t.Fatalf("first size = %dx%d", width, height)
	}
	if width, height := loadWindowSize(second, 1440, 900, 1900, 1000); width != 920 || height != 620 {
		t.Fatalf("second size = %dx%d", width, height)
	}
}

func TestWindowSizeFallsBackAndClampsToCurrentScreen(t *testing.T) {
	dir := t.TempDir()
	if width, height := loadWindowSize(dir, 1400, 860, 1500, 900); width != 1400 || height != 860 {
		t.Fatalf("fallback size = %dx%d", width, height)
	}
	if err := os.WriteFile(filepath.Join(dir, windowStateFilename), []byte(`{"width":2400,"height":1200}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if width, height := loadWindowSize(dir, 1400, 860, 1500, 900); width != 1500 || height != 900 {
		t.Fatalf("clamped size = %dx%d", width, height)
	}
}

func TestWindowSizeRejectsInvalidSave(t *testing.T) {
	if err := saveWindowSize(t.TempDir(), 400, 300); err == nil {
		t.Fatal("saveWindowSize accepted an unusable size")
	}
}

func TestWindowStateRemembersMaximizedSeparately(t *testing.T) {
	dir := t.TempDir()
	if err := saveWindowState(dir, 1100, 700, true); err != nil {
		t.Fatal(err)
	}
	state := loadWindowState(dir, 1440, 900, 1900, 1000)
	if state.Width != 1100 || state.Height != 700 || !state.Maximized {
		t.Fatalf("state = %+v", state)
	}
}
