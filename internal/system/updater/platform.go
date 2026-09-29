package updater

import "runtime"

func newManager(dir, version string) *Manager {
	return &Manager{Dir: dir, CurrentVersion: version, OS: runtime.GOOS, Arch: runtime.GOARCH, LatestURL: LatestURL}
}
