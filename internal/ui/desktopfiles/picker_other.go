//go:build !windows

package desktopfiles

import "errors"

func Available() bool { return false }
func Pick(string, string) ([]string, error) {
	return nil, errors.New("native file picker is currently available on Windows")
}
func ClipboardFiles(int) ([]string, error) { return nil, nil }

func HasClipboardImage() bool { return false }
func ClipboardPNG() ([]byte, error) {
	return nil, errors.New("clipboard images are currently available on Windows")
}
