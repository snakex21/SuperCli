package webgui

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	llmprompt "supercli/internal/llm/prompt"
)

func buildDataExport(dataDir string) (string, error) {
	return buildDataExportMode(dataDir, false)
}

func buildFullDataExport(dataDir string) (string, error) {
	return buildDataExportMode(dataDir, true)
}

func buildDataExportMode(dataDir string, full bool) (string, error) {
	stage, err := os.MkdirTemp("", "supercli-export-*")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) { os.RemoveAll(stage); return "", err }
	dataRoot := filepath.Join(stage, "data")
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return fail(err)
	}
	for _, name := range []string{"sessions.db", "memory.db", "supercli.db"} {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err == nil {
			if err := snapshotSQLite(src, filepath.Join(dataRoot, name)); err != nil {
				return fail(err)
			}
		}
	}
	for _, name := range []string{"projects.json", "workspace.json", "webgui-settings.json", llmprompt.UserInstructionsFile, folderIndexFile, folderIndexCacheFile, "schedules.json"} {
		if err := copyIfExists(filepath.Join(dataDir, name), filepath.Join(dataRoot, name)); err != nil {
			return fail(err)
		}
	}
	if err := copyTreeIfExists(filepath.Join(dataDir, "memory"), filepath.Join(dataRoot, "memory")); err != nil {
		return fail(err)
	}
	if err := copyTreeIfExists(filepath.Join(dataDir, "reflect"), filepath.Join(dataRoot, "reflect")); err != nil {
		return fail(err)
	}
	if err := copyTreeIfExists(filepath.Join(dataDir, "module-sources"), filepath.Join(dataRoot, "module-sources")); err != nil {
		return fail(err)
	}
	projectsRoot := filepath.Join(dataDir, "projects")
	entries, _ := os.ReadDir(projectsRoot)
	for _, entry := range entries {
		if !entry.IsDir() || !safeDataName(entry.Name()) {
			continue
		}
		srcRoot := filepath.Join(projectsRoot, entry.Name())
		dstRoot := filepath.Join(dataRoot, "projects", entry.Name())
		if _, err := os.Stat(filepath.Join(srcRoot, "memory.db")); err == nil {
			if err := snapshotSQLite(filepath.Join(srcRoot, "memory.db"), filepath.Join(dstRoot, "memory.db")); err != nil {
				return fail(err)
			}
		}
		if err := copyTreeIfExists(filepath.Join(srcRoot, "memory"), filepath.Join(dstRoot, "memory")); err != nil {
			return fail(err)
		}
	}
	if full {
		for _, name := range []string{"config.toml", "models.json", "context_limits.json"} {
			if err := copyIfExists(filepath.Join(dataDir, name), filepath.Join(dataRoot, name)); err != nil {
				return fail(err)
			}
		}
		rootEntries, readErr := os.ReadDir(dataDir)
		if readErr != nil {
			return fail(readErr)
		}
		for _, entry := range rootEntries {
			if entry.IsDir() || !isAuthBackupName(entry.Name()) {
				continue
			}
			if err := copyIfExists(filepath.Join(dataDir, entry.Name()), filepath.Join(dataRoot, entry.Name())); err != nil {
				return fail(err)
			}
		}
		for _, name := range []string{"mcp", "skills", "tools", "profiles"} {
			if err := copyTreeIfExists(filepath.Join(dataDir, name), filepath.Join(dataRoot, name)); err != nil {
				return fail(err)
			}
		}
	}
	manifest, _ := json.MarshalIndent(dataBackupMeta{Format: dataBackupFormat, CreatedAt: time.Now().UTC(), App: "SuperCli", Secrets: full}, "", "  ")
	if err := os.WriteFile(filepath.Join(stage, dataBackupManifest), manifest, 0o600); err != nil {
		return fail(err)
	}
	return stage, nil
}

func snapshotSQLite(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_ = os.Remove(dst)
	db, err := sql.Open("sqlite", src+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	quoted := strings.ReplaceAll(filepath.ToSlash(dst), "'", "''")
	if _, err := db.Exec("VACUUM INTO '" + quoted + "'"); err != nil {
		return fmt.Errorf("snapshot %s: %w", filepath.Base(src), err)
	}
	return nil
}
