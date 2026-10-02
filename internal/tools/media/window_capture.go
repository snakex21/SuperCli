package media

import (
	"fmt"
	"strconv"
	"strings"
)

// WindowSelector identifies one already-open top-level window. A title is an
// exact, case-insensitive match first, then an unambiguous substring match.
// HWND is a decimal or 0x-prefixed Windows handle; it is never a foreground hint.
// PID restricts selection to that process, optionally narrowed by title/handle.
type WindowSelector struct {
	Title string
	HWND  string
	PID   uint32
}

// WindowInfo describes the selected window without exposing its pixels.
type WindowInfo struct {
	HWND      string `json:"hwnd"`
	Title     string `json:"title"`
	PID       uint32 `json:"pid"`
	Minimized bool   `json:"minimized"`
	Visible   bool   `json:"visible"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	auxiliary bool   // Native input/tool windows are not automatic PID targets.
}

func parseWindowHandle(value string) (uintptr, error) {
	value = strings.TrimSpace(value)
	base := 10
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		value, base = value[2:], 16
	}
	v, err := strconv.ParseUint(value, base, strconv.IntSize)
	if err != nil || v == 0 {
		return 0, fmt.Errorf("window_id must be a nonzero decimal or 0x-prefixed window handle")
	}
	return uintptr(v), nil
}

func validateWindowSelector(selector WindowSelector) error {
	if strings.TrimSpace(selector.Title) == "" && strings.TrimSpace(selector.HWND) == "" && selector.PID == 0 {
		return fmt.Errorf("window capture requires process_id, window_title or window_id; use source:windows to list open windows")
	}
	if strings.TrimSpace(selector.Title) != "" && strings.TrimSpace(selector.HWND) != "" {
		return fmt.Errorf("choose either window_title or window_id")
	}
	if strings.TrimSpace(selector.HWND) != "" {
		_, err := parseWindowHandle(selector.HWND)
		return err
	}
	return nil
}

func selectCaptureWindow(windows []WindowInfo, selector WindowSelector) (WindowInfo, error) {
	selector.Title = strings.TrimSpace(selector.Title)
	selector.HWND = strings.TrimSpace(selector.HWND)
	if err := validateWindowSelector(selector); err != nil {
		return WindowInfo{}, err
	}
	if selector.HWND != "" {
		hwnd, _ := parseWindowHandle(selector.HWND)
		for _, window := range windows {
			id, err := parseWindowHandle(window.HWND)
			if err == nil && id == hwnd {
				if selector.PID != 0 && window.PID != selector.PID {
					return WindowInfo{}, fmt.Errorf("window_id %q does not belong to process_id %d", selector.HWND, selector.PID)
				}
				return window, nil
			}
		}
		return WindowInfo{}, fmt.Errorf("window_id %q no longer identifies an open top-level window", selector.HWND)
	}
	title := strings.TrimSpace(selector.Title)
	var exact, partial []WindowInfo
	for _, window := range windows {
		if selector.PID != 0 && window.PID != selector.PID {
			continue
		}
		if title == "" {
			if window.auxiliary {
				continue
			}
			partial = append(partial, window)
			continue
		}
		if strings.EqualFold(window.Title, title) {
			exact = append(exact, window)
		}
		if strings.Contains(strings.ToLower(window.Title), strings.ToLower(title)) {
			partial = append(partial, window)
		}
	}
	matches := partial
	if len(exact) > 0 {
		matches = exact
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		if selector.PID != 0 {
			return WindowInfo{}, fmt.Errorf("process_id %d has no matching open window (title %q); launch the GUI executable directly, or use headless_control for a browser/VM", selector.PID, title)
		}
		return WindowInfo{}, fmt.Errorf("no open window matches title %q; use source:windows to list open windows", title)
	}
	choices := make([]string, 0, 8)
	for i, match := range matches {
		if i == 8 {
			break
		}
		caption := []rune(match.Title)
		if len(caption) > 120 {
			caption = append(caption[:117], '.', '.', '.')
		}
		choices = append(choices, fmt.Sprintf("%s (%q)", match.HWND, string(caption)))
	}
	if selector.PID != 0 {
		return WindowInfo{}, fmt.Errorf("process_id %d has ambiguous windows (%d matches); narrow window_title or window_id: %s", selector.PID, len(matches), strings.Join(choices, ", "))
	}
	return WindowInfo{}, fmt.Errorf("window title %q is ambiguous (%d matches); choose window_id: %s", title, len(matches), strings.Join(choices, ", "))
}
