//go:build !windows

package webgui

import "errors"

func runNativeAppWindow(string) error {
	return errors.New("native WebView2 window is only available on Windows")
}
