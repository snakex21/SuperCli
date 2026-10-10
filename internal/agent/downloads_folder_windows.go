//go:build windows

package agent

import "golang.org/x/sys/windows"

// SystemDownloadsDir resolves the actual known folder, including redirection.
// It is used only for user-requested output, never for application data.
func SystemDownloadsDir() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
}
