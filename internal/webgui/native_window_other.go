//go:build !windows

package webgui

import "errors"

func runNativeAppWindow(string, string, string, string, func() bool) error {
	return errors.New("native WebView2 window is only available on Windows")
}
