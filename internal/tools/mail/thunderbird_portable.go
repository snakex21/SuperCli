package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"supercli/internal/storage"
)

func thunderbirdRuntimeDataDir(dataDir string) (string, error) {
	root, _, err := storage.ResolveRuntimeDataRoot(dataDir)
	return root, err
}

func thunderbirdCacheDir(dataDir, kind string) (string, error) {
	root, err := thunderbirdRuntimeDataDir(dataDir)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "cache", "thunderbird", kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// The shared bridge keeps the root of its active app instance. Constructors
// remain lazy, and a second instance cannot redirect an already active cache.
func (b *thunderbirdBridgeState) configureDataDir(dataDir string) error {
	root, err := thunderbirdRuntimeDataDir(dataDir)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dataDir == "" {
		b.dataDir = root
		return nil
	}
	same := filepath.Clean(b.dataDir) == filepath.Clean(root)
	if runtime.GOOS == "windows" {
		same = strings.EqualFold(filepath.Clean(b.dataDir), filepath.Clean(root))
	}
	if !same {
		return fmt.Errorf("thunderbird bridge is already configured for another application data directory")
	}
	return nil
}

func (b *thunderbirdBridgeState) attachmentCacheDir() (string, error) {
	b.mu.Lock()
	root := b.dataDir
	b.mu.Unlock()
	if root == "" {
		if err := b.configureDataDir(""); err != nil {
			return "", err
		}
		b.mu.Lock()
		root = b.dataDir
		b.mu.Unlock()
	}
	return thunderbirdCacheDir(root, "attachments")
}

func createThunderbirdMSGFile(dataDir string) (*os.File, error) {
	dir, err := thunderbirdCacheDir(dataDir, "msg-import")
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "message-*.eml")
}

func createThunderbirdMSGAttachmentDir(destination string) (string, error) {
	if strings.TrimSpace(destination) == "" {
		return "", fmt.Errorf("MSG destination is empty")
	}
	return os.MkdirTemp(filepath.Dir(destination), "attachments-*")
}
