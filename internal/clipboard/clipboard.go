// Package clipboard writes text to the OS clipboard by shelling
// out to the platform's native utility. It is the write-text
// counterpart to the image-read clipboard shim used by
// send_screenshot: pure Go, no cgo, no syscalls.
//
// Text is always passed on the child process's STDIN, never as a
// command-line argument — this avoids shell-injection surface and
// the OS argument-length limit, so arbitrarily large exports copy
// safely.
package clipboard

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// WriteText copies s to the OS clipboard. It returns an error
// describing the failure (utility missing, non-zero exit) so the
// caller can fall back to a file. An empty string is allowed and
// clears the clipboard on most platforms.
func WriteText(s string) error {
	name, args, err := clipboardCommand()
	if err != nil {
		return err
	}
	return runClipboard(name, args, s)
}

// runClipboard pipes s to the named command's stdin and reports a
// readable error including any stderr text.
func runClipboard(name string, args []string, s string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(s)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("clipboard: %s failed: %v: %s", name, err, msg)
		}
		return fmt.Errorf("clipboard: %s failed: %v", name, err)
	}
	return nil
}

// clipboardCommand returns the platform's clipboard-write command.
// On Linux it prefers wl-copy (Wayland) then xclip (X11); the
// caller sees a clear error if neither is installed.
func clipboardCommand() (string, []string, error) {
	switch runtime.GOOS {
	case "windows":
		// clip.exe reads stdin and sets the clipboard.
		return "clip", nil, nil
	case "darwin":
		return "pbcopy", nil, nil
	case "linux":
		if path, err := exec.LookPath("wl-copy"); err == nil {
			return path, nil, nil
		}
		if path, err := exec.LookPath("xclip"); err == nil {
			return path, []string{"-selection", "clipboard"}, nil
		}
		return "", nil, fmt.Errorf("clipboard: no utility found (install wl-clipboard or xclip)")
	default:
		return "", nil, fmt.Errorf("clipboard: unsupported platform %q", runtime.GOOS)
	}
}
