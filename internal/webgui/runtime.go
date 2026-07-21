package webgui

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/buildinfo"
	"supercli/internal/tools/sandbox"
)

// NativeAppVersion mirrors the shared build metadata. It remains a variable so
// branded launchers can report the engine version without maintaining a second
// hard-coded value.
var NativeAppVersion = buildinfo.Version

// UIContractVersion is the compatibility boundary used by replaceable branded
// launchers. Bump it only when an existing overlay can no longer use the
// shared browser runtime or HTTP/SSE event contract.
const UIContractVersion = 1

func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now().UTC()
	appName := s.runtimeAppName()
	writeJSON(w, map[string]any{
		"app":                    appName,
		"engine":                 "SuperCli",
		"version":                NativeAppVersion,
		"ui_contract":            UIContractVersion,
		"started_at":             s.startedAt,
		"uptime_seconds":         int64(now.Sub(s.startedAt).Seconds()),
		"status":                 "running",
		"full_filesystem_access": sandbox.IsUnsandboxed(),
		"update_supported":       false,
	})
}

func (s *Server) handleRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logsRoot := filepath.Join(s.eng.DataDir(), "logs")
	entries, err := os.ReadDir(logsRoot)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, strings.ToLower(s.runtimeAppName())+"-logs-"+time.Now().Format("20060102-150405")+".zip"),
	)
	archive := zip.NewWriter(w)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !safeDataName(name) || !isLogExportName(name) {
			continue
		}
		full := filepath.Join(logsRoot, name)
		info, statErr := entry.Info()
		if statErr != nil || info.Size() > 32<<20 {
			continue
		}
		source, openErr := os.Open(full)
		if openErr != nil {
			continue
		}
		target, createErr := archive.Create(name)
		if createErr == nil {
			_, createErr = io.Copy(target, io.LimitReader(source, 32<<20))
		}
		_ = source.Close()
		if createErr != nil {
			_ = archive.Close()
			return
		}
	}
	_ = archive.Close()
}

func (s *Server) runtimeAppName() string {
	appName := strings.TrimSpace(s.appName)
	if appName == "" {
		return "SuperCli"
	}
	return appName
}

func isLogExportName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".log" || ext == ".txt" || ext == ".jsonl"
}
