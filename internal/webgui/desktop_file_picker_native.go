package webgui

import "supercli/internal/ui/desktopfiles"

func pickDesktopFiles(initialDir, language string) ([]string, error) {
	return desktopfiles.Pick(initialDir, language)
}
