package webgui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/storage/memory"
)

func countAllMemory(dataDir string) (int, error) {
	total := 0
	roots := []string{dataDir}
	projects, _ := os.ReadDir(filepath.Join(dataDir, "projects"))
	for _, entry := range projects {
		if entry.IsDir() && safeDataName(entry.Name()) {
			roots = append(roots, filepath.Join(dataDir, "projects", entry.Name()))
		}
	}
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, "memory.db")); os.IsNotExist(err) {
			continue
		}
		store, err := memory.OpenStore(root)
		if err != nil {
			return total, err
		}
		entries, listErr := store.List("", 0)
		closeErr := store.Close()
		if listErr != nil || closeErr != nil {
			return total, errors.Join(listErr, closeErr)
		}
		total += len(entries)
	}
	return total, nil
}

func clearAllMemory(dataDir string) (int, error) {
	total := 0
	roots := []string{dataDir}
	projects, _ := os.ReadDir(filepath.Join(dataDir, "projects"))
	for _, entry := range projects {
		if entry.IsDir() && safeDataName(entry.Name()) {
			roots = append(roots, filepath.Join(dataDir, "projects", entry.Name()))
		}
	}
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, "memory.db")); os.IsNotExist(err) {
			continue
		}
		store, err := memory.OpenStore(root)
		if err != nil {
			return total, err
		}
		removed, clearErr := store.Clear()
		closeErr := store.Close()
		if clearErr != nil || closeErr != nil {
			return total, errors.Join(clearErr, closeErr)
		}
		total += removed
	}
	return total, nil
}

func copyIfExists(src, dst string) error {
	in, err := os.Open(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func copyTreeIfExists(src, dst string) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return copyIfExists(path, target)
	})
}

func pathInside(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func safeDataName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\<>:"|?*`) {
		return false
	}
	for _, r := range name {
		if r < 32 {
			return false
		}
	}
	return true
}

func isAuthBackupName(name string) bool {
	if name == "auth.json" {
		return true
	}
	if !strings.HasPrefix(name, "auth-") || !strings.HasSuffix(name, ".json") || !safeDataName(name) {
		return false
	}
	label := strings.TrimSuffix(strings.TrimPrefix(name, "auth-"), ".json")
	return label != ""
}

func randomDataID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
