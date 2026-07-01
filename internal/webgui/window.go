package webgui

import (
	"os/exec"
	"runtime"
	"syscall"
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
func OpenAppWindow(url string) (*exec.Cmd, error) {
	browsers := chromiumCandidates()
	flag := "--app=" + url
	for _, b := range browsers {
		path, err := exec.LookPath(b)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, flag, "--new-window")
		if err := cmd.Start(); err != nil {
			continue
		}
		return cmd, nil
	}
	return nil, errNoBrowser
}

// OpenInBrowser opens url in the user's default browser as a normal
// tab. It is the fallback when no Chromium app-mode browser is found,
// so the GUI is still reachable.
func OpenInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
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
