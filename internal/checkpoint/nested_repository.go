package checkpoint

import (
	"os"
	"path/filepath"
)

// A .git directory or gitfile marks a separate repository's ownership boundary.
// The workspace's own Git metadata does not exclude the workspace itself.
func (m *Manager) nestedRepositoryDirectory(dir string) (bool, error) {
	if pathEqual(dir, m.home) {
		return false, nil
	}
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
