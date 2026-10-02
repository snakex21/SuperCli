//go:build linux || darwin

package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

func openURL(normalized string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	cmd := exec.Command(command, normalized)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	// Reap the launcher even when it remains attached to the browser's lifetime.
	go func() { _ = cmd.Wait() }()
	return nil
}
