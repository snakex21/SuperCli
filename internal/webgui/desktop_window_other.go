//go:build !windows

package webgui

import "errors"

func runNativeAppWindow(nativeWindowOptions) error {
	return errors.New("native WebView2 window is only available on Windows")
}
