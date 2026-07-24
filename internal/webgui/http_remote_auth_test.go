package webgui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRemoteSessionTokenFileKeepsSecretOutOfPathAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("a1", 32)
	path, cleanup, err := writeRemoteSessionTokenFile(dir, token)
	if err != nil {
		t.Fatal(err)
	}
	if !pathInside(dir, path) {
		t.Fatalf("token path escaped data directory: %s", path)
	}
	if strings.Contains(filepath.Base(path), token) {
		t.Fatal("token leaked into file name")
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != token {
		t.Fatalf("token file=%q err=%v", data, err)
	}
	assertTokenFileRestricted(t, path)
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file remains after cleanup: %v", err)
	}
}
