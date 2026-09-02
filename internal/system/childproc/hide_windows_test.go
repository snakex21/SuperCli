//go:build windows

package childproc

import (
	"os/exec"
	"testing"
)

func TestNoConsoleWindowKeepsFirstGUIWindowVisible(t *testing.T) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "exit 0")
	NoConsoleWindow(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr was not initialized")
	}
	if cmd.SysProcAttr.HideWindow {
		t.Fatal("NoConsoleWindow must not set HideWindow; that can hide a native picker")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("CREATE_NO_WINDOW was not enabled")
	}
}
