//go:build windows

package webgui

import "os"

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
