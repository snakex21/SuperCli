package webgui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
