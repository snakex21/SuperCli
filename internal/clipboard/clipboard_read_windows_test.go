//go:build windows

package clipboard

import (
	"bytes"
	"os/exec"
	"strings"
)

// readClipboardWindowsForTest reads the clipboard text back via
// PowerShell so the round-trip test can verify WriteText. Test-only
// helper; lives behind the windows build tag.
func readClipboardWindowsForTest() (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}
