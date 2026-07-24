package webgui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	llmprompt "supercli/internal/llm/prompt"
)

func ApplyPendingDataImport(dataDir string) error {
	markerPath := filepath.Join(dataDir, pendingImportFile)
	pending, err := readPendingDataImport(markerPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	stage := filepath.Clean(pending.Stage)
	importsRoot := filepath.Join(dataDir, "imports")
	if !pathInside(importsRoot, stage) {
		return errors.New("pending import points outside the imports directory")
	}
	dataStage := filepath.Join(stage, "data")
	if info, err := os.Stat(dataStage); err != nil || !info.IsDir() {
		return errors.New("pending import data is missing")
	}
	rescue := filepath.Join(dataDir, "backups", "pre-import-"+time.Now().Format("20060102-150405")+"-"+randomDataID()[:6])
	if err := os.MkdirAll(rescue, 0o700); err != nil {
		return err
	}
	targets := dataImportTargets(dataDir, pending.Full)
	moved := []string{}
	for _, name := range targets {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(rescue, name)); err != nil {
			restoreMovedData(dataDir, rescue, moved)
			return fmt.Errorf("prepare import %s: %w", name, err)
		}
		moved = append(moved, name)
	}
	installed := []string{}
	entries, err := os.ReadDir(dataStage)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !safeDataName(name) || !allowedImportedRoot(name, pending.Full) {
				err = fmt.Errorf("unsafe imported name %q", name)
				break
			}
			if renameErr := os.Rename(filepath.Join(dataStage, name), filepath.Join(dataDir, name)); renameErr != nil {
				err = renameErr
				break
			}
			installed = append(installed, name)
		}
	}
	if err != nil {
		for _, name := range installed {
			_ = os.RemoveAll(filepath.Join(dataDir, name))
		}
		restoreMovedData(dataDir, rescue, moved)
		return fmt.Errorf("apply import: %w", err)
	}
	_ = os.Remove(markerPath)
	_ = os.RemoveAll(stage)
	return nil
}

func dataImportTargets(dataDir string, full bool) []string {
	targets := []string{"sessions.db", "sessions.db-wal", "sessions.db-shm", "memory.db", "memory.db-wal", "memory.db-shm", "supercli.db", "supercli.db-wal", "supercli.db-shm", "memory", "projects", "reflect", "module-sources", "checkpoints", "projects.json", "workspace.json", "webgui-settings.json", llmprompt.UserInstructionsFile, folderIndexFile, folderIndexCacheFile, "schedules.json"}
	if !full {
		return targets
	}
	targets = append(targets, "config.toml", "models.json", "context_limits.json", "mcp", "skills", "tools", "profiles")
	entries, _ := os.ReadDir(dataDir)
	for _, entry := range entries {
		if !entry.IsDir() && isAuthBackupName(entry.Name()) {
			targets = append(targets, entry.Name())
		}
	}
	return targets
}

func allowedImportedRoot(name string, full bool) bool {
	switch name {
	case "sessions.db", "memory.db", "supercli.db", "memory", "projects", "reflect", "module-sources", "projects.json", "workspace.json", "webgui-settings.json", llmprompt.UserInstructionsFile, folderIndexFile, folderIndexCacheFile, "schedules.json":
		return true
	case "config.toml", "models.json", "context_limits.json", "mcp", "skills", "tools", "profiles":
		return full
	default:
		return full && isAuthBackupName(name)
	}
}

func restoreMovedData(dataDir, rescue string, names []string) {
	for i := len(names) - 1; i >= 0; i-- {
		_ = os.Rename(filepath.Join(rescue, names[i]), filepath.Join(dataDir, names[i]))
	}
}

func readPendingDataImport(path string) (pendingDataImport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pendingDataImport{}, err
	}
	var pending pendingDataImport
	if err := json.Unmarshal(data, &pending); err != nil {
		return pendingDataImport{}, err
	}
	return pending, nil
}

func removeImportStage(dataDir, stage string) error {
	if pathInside(filepath.Join(dataDir, "imports"), stage) {
		return os.RemoveAll(stage)
	}
	return nil
}
