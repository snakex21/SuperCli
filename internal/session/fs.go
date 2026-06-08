package session

import (
	"os"
)

// mkdirAll is a thin wrapper used by OpenStore.
func mkdirAll(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	return nil
}

// tempHome creates a fresh temp directory.
func tempHome() (string, error) {
	return os.MkdirTemp("", "supercli-sess-*")
}
