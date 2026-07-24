package webgui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeRemoteSessionTokenFile gives an operator a local, permission-restricted
// handoff for the ephemeral remote password. The secret is never placed in a
// URL or log; remote browsers use it as the HTTP Basic password. The caller
// must invoke cleanup when the process exits.
func writeRemoteSessionTokenFile(dataDir, token string) (path string, cleanup func(), err error) {
	if strings.TrimSpace(token) == "" {
		return "", nil, fmt.Errorf("remote session token is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("prepare remote token directory: %w", err)
	}
	file, err := os.CreateTemp(dataDir, ".supercli-remote-session-*.token")
	if err != nil {
		return "", nil, fmt.Errorf("create remote token file: %w", err)
	}
	path = filepath.Clean(file.Name())
	remove := func() { _ = os.Remove(path) }
	fail := func(cause error) (string, func(), error) {
		_ = file.Close()
		remove()
		return "", nil, cause
	}
	if err := restrictTokenFile(path); err != nil {
		return fail(fmt.Errorf("protect remote token file: %w", err))
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		return fail(fmt.Errorf("write remote token file: %w", err))
	}
	if err := file.Sync(); err != nil {
		return fail(fmt.Errorf("flush remote token file: %w", err))
	}
	if err := file.Close(); err != nil {
		remove()
		return "", nil, fmt.Errorf("close remote token file: %w", err)
	}
	return path, remove, nil
}
