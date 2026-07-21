package webgui

import (
	"archive/zip"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/storage/memory"
)

const (
	dataBackupFormat   = 1
	maxDataBackupBytes = 512 << 20
	maxDataBackupFiles = 20000
	pendingImportFile  = "pending-data-import.json"
	dataBackupManifest = "manifest.json"
)

type dataBackupMeta struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	App       string    `json:"app"`
	Secrets   bool      `json:"secrets_included"`
}

type pendingDataImport struct {
	Stage     string    `json:"stage"`
	CreatedAt time.Time `json:"created_at"`
	Full      bool      `json:"full"`
}

type dataStatus struct {
	Sessions      int  `json:"sessions"`
	MemoryEntries int  `json:"memory_entries"`
	Goals         int  `json:"goals"`
	QueuedTasks   int  `json:"queued_tasks"`
	Schedules     int  `json:"schedules"`
	FolderSources int  `json:"folder_sources"`
	ImportPending bool `json:"import_pending"`
}

func (s *Server) handleDataStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status := dataStatus{}
	if store, err := s.eng.sessionStore(); err == nil {
		if rows, listErr := store.List(0); listErr == nil {
			status.Sessions = len(rows)
		}
	}
	status.MemoryEntries, _ = countAllMemory(s.eng.DataDir())
	if service, err := s.eng.goalService(r.Context()); err == nil {
		if goals, listErr := service.List(r.Context()); listErr == nil {
			status.Goals = len(goals)
		}
	}
	if tasks, err := s.eng.queuedTasks(r.Context()); err == nil {
		status.QueuedTasks = len(tasks)
	}
	if s.eng.schedules != nil {
		status.Schedules = len(s.eng.schedules.List(s.eng.Home()))
	}
	folderIndexMu.Lock()
	folderConfig := loadFolderIndexConfig(s.eng.DataDir())
	folderIndexMu.Unlock()
	status.FolderSources = len(folderConfig.SelectedPaths)
	_, err := os.Stat(filepath.Join(s.eng.DataDir(), pendingImportFile))
	status.ImportPending = err == nil
	writeJSON(w, status)
}

func (s *Server) handleDataClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "sessions":
		store, err := s.eng.sessionStore()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows, err := store.List(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := store.DeleteAll(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.eng.clearCheckpointManagers(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "removed": len(rows)})
	case "memory":
		removed, err := clearAllMemory(s.eng.DataDir())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.RemoveAll(filepath.Join(s.eng.DataDir(), "reflect")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "removed": removed})
	default:
		http.Error(w, "action must be sessions or memory", http.StatusBadRequest)
	}
}

func (s *Server) handleDataExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	stage, err := buildDataExport(s.eng.DataDir())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(stage)
	name := "supercli-backup-" + time.Now().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	if err := writeZip(w, stage); err != nil {
		return
	}
}

func (s *Server) handleDataExportFull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	stage, err := buildFullDataExport(s.eng.DataDir())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(stage)
	name := "supercli-full-backup-" + time.Now().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	if err := writeZip(w, stage); err != nil {
		return
	}
}

// ExportDataBackup writes a portable backup to <dataDir>/backups and returns
// its absolute path. It is shared by the TUI and the web download handlers so
// both surfaces use the same SQLite snapshot and allow-list rules. When full
// is true provider credentials and portable MCP/skill packages are included.
func ExportDataBackup(dataDir string, full bool) (string, error) {
	var (
		stage string
		err   error
	)
	if full {
		stage, err = buildFullDataExport(dataDir)
	} else {
		stage, err = buildDataExport(dataDir)
	}
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	prefix := "supercli-backup-"
	if full {
		prefix = "supercli-full-backup-"
	}
	dst := filepath.Join(backupDir, prefix+time.Now().Format("20060102-150405")+".zip")
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	writeErr := writeZip(file, stage)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// StageDataImport validates and extracts a backup into the private imports
// directory. Nothing live is replaced here: ApplyPendingDataImport performs
// the atomic swap on the next process start. The boolean reports whether the
// archive is a full backup containing credentials.
func StageDataImport(dataDir, archivePath string) (bool, error) {
	archivePath = filepath.Clean(strings.TrimSpace(archivePath))
	info, err := os.Stat(archivePath)
	if err != nil {
		return false, err
	}
	if info.IsDir() || info.Size() > maxDataBackupBytes {
		return false, errors.New("backup must be a ZIP file smaller than 512 MiB")
	}
	meta, err := readDataBackupMeta(archivePath)
	if err != nil {
		return false, err
	}
	importsRoot := filepath.Join(dataDir, "imports")
	if err := os.MkdirAll(importsRoot, 0o700); err != nil {
		return false, err
	}
	id := randomDataID()
	stage := filepath.Join(importsRoot, id)
	meta, err = extractDataBackupMode(archivePath, stage, meta.Secrets)
	if err != nil {
		_ = os.RemoveAll(stage)
		return false, err
	}
	markerPath := filepath.Join(dataDir, pendingImportFile)
	if old, readErr := readPendingDataImport(markerPath); readErr == nil {
		_ = removeImportStage(dataDir, old.Stage)
	}
	marker := pendingDataImport{Stage: stage, CreatedAt: time.Now().UTC(), Full: meta.Secrets}
	data, _ := json.MarshalIndent(marker, "", "  ")
	if err := os.WriteFile(markerPath, data, 0o600); err != nil {
		_ = os.RemoveAll(stage)
		return false, err
	}
	return meta.Secrets, nil
}

func (s *Server) handleDataImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	r.Body = http.MaxBytesReader(w, r.Body, maxDataBackupBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "invalid backup upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		http.Error(w, "backup file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	importsRoot := filepath.Join(s.eng.DataDir(), "imports")
	if err := os.MkdirAll(importsRoot, 0o700); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id := randomDataID()
	uploadPath := filepath.Join(importsRoot, id+".zip")
	upload, err := os.OpenFile(uploadPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, copyErr := io.Copy(upload, file)
	closeErr := upload.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(uploadPath)
		http.Error(w, errors.Join(copyErr, closeErr).Error(), http.StatusBadRequest)
		return
	}
	defer os.Remove(uploadPath)
	full, err := StageDataImport(s.eng.DataDir(), uploadPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "restart_required": true, "full": full})
}

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

func writeZip(dst io.Writer, root string) error {
	zw := zip.NewWriter(dst)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	return errors.Join(err, zw.Close())
}

func readDataBackupMeta(archivePath string) (dataBackupMeta, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataBackupMeta{}, fmt.Errorf("invalid backup archive: %w", err)
	}
	defer zr.Close()
	for _, file := range zr.File {
		if filepath.ToSlash(file.Name) != dataBackupManifest || file.Mode()&os.ModeSymlink != 0 || file.UncompressedSize64 > 64<<10 {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return dataBackupMeta{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, (64<<10)+1))
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			return dataBackupMeta{}, errors.Join(readErr, closeErr)
		}
		if len(data) > 64<<10 {
			return dataBackupMeta{}, errors.New("backup manifest is too large")
		}
		var manifest dataBackupMeta
		if err := json.Unmarshal(data, &manifest); err != nil || manifest.Format != dataBackupFormat || manifest.App != "SuperCli" {
			return dataBackupMeta{}, errors.New("unsupported backup format")
		}
		return manifest, nil
	}
	return dataBackupMeta{}, errors.New("backup manifest is missing")
}

func extractDataBackup(archivePath, stage string) error {
	_, err := extractDataBackupMode(archivePath, stage, false)
	return err
}

func extractDataBackupMode(archivePath, stage string, allowSecrets bool) (dataBackupMeta, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataBackupMeta{}, fmt.Errorf("invalid backup archive: %w", err)
	}
	defer zr.Close()
	if len(zr.File) == 0 || len(zr.File) > maxDataBackupFiles {
		return dataBackupMeta{}, fmt.Errorf("backup contains an invalid number of files")
	}
	var total uint64
	var writtenTotal int64
	for _, file := range zr.File {
		rawName := filepath.ToSlash(file.Name)
		name := path.Clean(rawName)
		if rawName != name || !allowedBackupPathMode(name, allowSecrets) || file.Mode()&os.ModeSymlink != 0 {
			return dataBackupMeta{}, fmt.Errorf("backup contains unsupported path %q", file.Name)
		}
		total += file.UncompressedSize64
		if total > maxDataBackupBytes {
			return dataBackupMeta{}, fmt.Errorf("unpacked backup is too large")
		}
		full := filepath.Join(stage, filepath.FromSlash(name))
		if !pathInside(stage, full) {
			return dataBackupMeta{}, fmt.Errorf("unsafe backup path %q", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(full, 0o700); err != nil {
				return dataBackupMeta{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return dataBackupMeta{}, err
		}
		rc, err := file.Open()
		if err != nil {
			return dataBackupMeta{}, err
		}
		out, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rc.Close()
			return dataBackupMeta{}, err
		}
		remaining := int64(maxDataBackupBytes) - writtenTotal
		written, copyErr := io.Copy(out, io.LimitReader(rc, remaining+1))
		writtenTotal += written
		if written > remaining {
			copyErr = errors.Join(copyErr, errors.New("unpacked backup is too large"))
		}
		err = errors.Join(copyErr, out.Close(), rc.Close())
		if err != nil {
			return dataBackupMeta{}, err
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(stage, dataBackupManifest))
	if err != nil {
		return dataBackupMeta{}, errors.New("backup manifest is missing")
	}
	var manifest dataBackupMeta
	if err := json.Unmarshal(manifestData, &manifest); err != nil || manifest.Format != dataBackupFormat || manifest.App != "SuperCli" || manifest.Secrets != allowSecrets {
		return dataBackupMeta{}, errors.New("unsupported or unsafe backup format")
	}
	return manifest, nil
}

func allowedBackupPath(name string) bool {
	return allowedBackupPathMode(name, false)
}

func allowedBackupPathMode(name string, allowSecrets bool) bool {
	if name == dataBackupManifest {
		return true
	}
	if !strings.HasPrefix(name, "data/") || strings.Contains(name, "\\") {
		return false
	}
	rel := strings.TrimPrefix(name, "data/")
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if !safeDataName(part) {
			return false
		}
	}
	switch rel {
	case "sessions.db", "memory.db", "supercli.db", "projects.json", "workspace.json", "webgui-settings.json", llmprompt.UserInstructionsFile, folderIndexFile, folderIndexCacheFile, "schedules.json":
		return true
	}
	if len(parts) >= 2 && (parts[0] == "memory" || parts[0] == "reflect" || parts[0] == "module-sources") {
		return true
	}
	if len(parts) >= 3 && parts[0] == "projects" && safeDataName(parts[1]) {
		if parts[2] == "memory.db" {
			return len(parts) == 3
		}
		if parts[2] == "memory" && len(parts) >= 4 {
			return true
		}
	}
	if !allowSecrets {
		return false
	}
	if rel == "config.toml" || rel == "models.json" || rel == "context_limits.json" || (len(parts) == 1 && isAuthBackupName(rel)) {
		return true
	}
	if len(parts) >= 2 && (parts[0] == "mcp" || parts[0] == "skills" || parts[0] == "tools" || parts[0] == "profiles") {
		return true
	}
	return false
}

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
