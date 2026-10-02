package webgui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"supercli/internal/system/browser"
)

// OpenAppWindow tries to open url in a chromeless "app mode" window
// using a Chromium-family browser (Edge/Chrome/Chromium/Brave). This
// gives a desktop-app feel without CGO or a native webview library —
// the browser process is just a viewer for the local server.
//
// It returns the started *exec.Cmd on success so the caller can wait
// on it (closing the window then ends the program) or nil plus an
// error when no suitable browser was found. A nil error with a nil
// Cmd never happens: either a browser launched or err is set.
func OpenAppWindow(url, profileDir string) (*exec.Cmd, error) {
	profileDir, err := prepareAppWindowProfile(profileDir)
	if err != nil {
		return nil, err
	}
	browsers := chromiumCandidates()
	args := appWindowArgs(url, profileDir)
	for _, b := range browsers {
		path, err := exec.LookPath(b)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, args...)
		if err := cmd.Start(); err != nil {
			continue
		}
		return cmd, nil
	}
	return nil, errNoBrowser
}

// OpenInBrowser opens url in the user's default browser as a normal
// tab. This uses the browser's own profile; portable GUI startup must not
// invoke it automatically.
func OpenInBrowser(url string) error {
	return browser.Open(url)
}

// prepareAppWindowProfile must succeed before any browser process is started.
// An empty profile would silently use the browser's user-profile directory.
func prepareAppWindowProfile(profileDir string) (string, error) {
	if strings.TrimSpace(profileDir) == "" {
		return "", fmt.Errorf("portable browser profile directory is required")
	}
	profileDir, err := filepath.Abs(profileDir)
	if err != nil {
		return "", fmt.Errorf("resolve portable browser profile: %w", err)
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return "", fmt.Errorf("create portable browser profile: %w", err)
	}
	return profileDir, nil
}

func appWindowArgs(url, profileDir string) []string {
	args := []string{
		"--app=" + url,
		"--new-window",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-mode",
	}
	if profileDir != "" {
		// A dedicated profile prevents Edge/Chrome from handing the app window
		// to an already-running normal browser process and immediately exiting.
		// Run uses the child lifetime to know when the app window was closed.
		args = append(args, "--user-data-dir="+profileDir)
	}
	return args
}

// chromiumCandidates returns the executable names/paths to probe for
// app-mode support, ordered by likelihood per platform.
func chromiumCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"msedge",
			"chrome",
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	default:
		return []string{
			"google-chrome", "google-chrome-stable", "chromium",
			"chromium-browser", "microsoft-edge", "brave-browser",
		}
	}
}
