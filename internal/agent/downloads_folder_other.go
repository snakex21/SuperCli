//go:build !windows

package agent

import (
	"os"
	"path/filepath"
)

func SystemDownloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Downloads"), nil
}
