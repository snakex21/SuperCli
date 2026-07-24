package webgui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	windowStateFilename = "window-state.json"
	minWindowWidth      = 720
	minWindowHeight     = 520
)

type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized,omitempty"`
}

func loadWindowState(dataDir string, fallbackWidth, fallbackHeight, maxWidth, maxHeight int) windowState {
	state := windowState{}
	data, err := os.ReadFile(filepath.Join(dataDir, windowStateFilename))
	if err == nil {
		_ = json.Unmarshal(data, &state)
	}
	state.Width = clampWindowDimension(state.Width, fallbackWidth, minWindowWidth, maxWidth)
	state.Height = clampWindowDimension(state.Height, fallbackHeight, minWindowHeight, maxHeight)
	return state
}

func loadWindowSize(dataDir string, fallbackWidth, fallbackHeight, maxWidth, maxHeight int) (int, int) {
	state := loadWindowState(dataDir, fallbackWidth, fallbackHeight, maxWidth, maxHeight)
	return state.Width, state.Height
}

func clampWindowDimension(value, fallback, minimum, maximum int) int {
	if maximum < minimum {
		maximum = minimum
	}
	if value <= 0 {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func saveWindowSize(dataDir string, width, height int) error {
	return saveWindowState(dataDir, width, height, false)
}

func saveWindowState(dataDir string, width, height int, maximized bool) error {
	if width < minWindowWidth || height < minWindowHeight {
		return fmt.Errorf("invalid window size %dx%d", width, height)
	}
	data, err := json.Marshal(windowState{Width: width, Height: height, Maximized: maximized})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, windowStateFilename), data, 0o600)
}
