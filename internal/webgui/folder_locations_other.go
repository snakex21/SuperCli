//go:build !windows

package webgui

import (
	"os"
	"path/filepath"
)

func suggestedUserFolders() []folderIndexLocation {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []folderIndexLocation{
		{Label: "Pulpit", Path: filepath.Join(home, "Desktop"), Kind: "system"},
		{Label: "Dokumenty", Path: filepath.Join(home, "Documents"), Kind: "system"},
		{Label: "Pobrane", Path: filepath.Join(home, "Downloads"), Kind: "system"},
		{Label: "Obrazy", Path: filepath.Join(home, "Pictures"), Kind: "system"},
	}
}
