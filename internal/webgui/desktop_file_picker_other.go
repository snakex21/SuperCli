//go:build !windows

package webgui

import "fmt"

func pickDesktopFiles(string) ([]string, error) {
	return nil, fmt.Errorf("native file picker is currently available on Windows")
}
