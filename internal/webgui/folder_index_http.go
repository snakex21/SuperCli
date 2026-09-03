package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

func folderIndexPrompt(dataDir string) string {
	folderIndexMu.Lock()
	config := loadFolderIndexConfig(dataDir)
	folderIndexMu.Unlock()
	if !config.Enabled || (len(config.SelectedPaths) == 0 && !config.OutlookIndex) {
		return ""
	}
	var lines []string
	for _, path := range config.SelectedPaths {
		line := "- " + path
		if indexed, ok := config.Indexed[path]; ok {
			line += fmt.Sprintf(" (%d files indexed %s)", indexed.FileCount, indexed.IndexedAt)
		}
		lines = append(lines, line)
	}
	if config.OutlookIndex {
		line := "- Outlook: " + config.OutlookFolder
		if config.OutlookIndexed != nil {
			line += fmt.Sprintf(" (%d messages indexed %s)", config.OutlookIndexed.FileCount, config.OutlookIndexed.IndexedAt)
		}
		lines = append(lines, line)
	}
	return "[indexed_folders]\nThese folders have a searchable document index containing file paths, extracted Word/PDF/Excel/PowerPoint/text content and optional AI descriptions of documents and images. Use recall with subject, person, date, file or folder keywords first; open the exact source file only when more detail is needed. Never claim the index contains the complete original.\n" +
		strings.Join(lines, "\n") + "\n[/indexed_folders]"
}

func (s *Server) handleFolderIndexing(w http.ResponseWriter, r *http.Request) {
	folderIndexMu.Lock()
	config := loadFolderIndexConfig(s.eng.dataDir)
	folderIndexMu.Unlock()
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{
			"config":      config,
			"entries":     folderIndexEntries(config),
			"suggestions": folderIndexSuggestions(config),
			"job":         s.folderIndexJobSnapshot(),
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var request struct {
		Action         string   `json:"action"`
		Paths          []string `json:"paths,omitempty"`
		SelectedPaths  []string `json:"selected_paths,omitempty"`
		CustomPaths    []string `json:"custom_paths,omitempty"`
		VisualIndex    bool     `json:"visual_index,omitempty"`
		VisionProvider string   `json:"vision_provider,omitempty"`
		VisionModel    string   `json:"vision_model,omitempty"`
		Background     bool     `json:"background,omitempty"`
		OutlookIndex   bool     `json:"outlook_index,omitempty"`
		OutlookFolder  string   `json:"outlook_folder,omitempty"`
		OutlookMax     int      `json:"outlook_max_messages,omitempty"`
		// Accepted for compatibility with older packaged UIs. Image indexing is
		// intentionally uncapped, so the value is ignored.
		LegacyVisionMaxImages int `json:"vision_max_images,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch request.Action {
	case "cancel":
		job, canceled := s.cancelFolderIndexJob()
		writeJSON(w, map[string]any{"ok": canceled, "job": job})
	case "save":
		if job := s.folderIndexJobSnapshot(); job != nil && job.State == "running" {
			http.Error(w, "indexing is in progress; source settings can be changed once it finishes", http.StatusConflict)
			return
		}
		previousPaths := uniqueCleanPaths(append(append([]string{}, config.SelectedPaths...), config.CustomPaths...))
		previousOutlook := config.OutlookIndex
		previousOutlookFolder := config.OutlookFolder
		config.SelectedPaths = uniqueCleanPaths(request.SelectedPaths)
		config.CustomPaths = uniqueCleanPaths(request.CustomPaths)
		config.VisualIndex = strings.TrimSpace(request.VisionModel) != ""
		config.VisionProvider = strings.TrimSpace(request.VisionProvider)
		config.VisionModel = strings.TrimSpace(request.VisionModel)
		config.OutlookIndex = request.OutlookIndex
		config.OutlookFolder = strings.TrimSpace(request.OutlookFolder)
		config.OutlookMax = request.OutlookMax
		kept := map[string]bool{}
		for _, path := range append(append([]string{}, config.SelectedPaths...), config.CustomPaths...) {
			kept[strings.ToLower(filepath.Clean(path))] = true
		}
		cache := loadFolderIndexCache(s.eng.dataDir)
		cacheChanged := false
		for _, path := range previousPaths {
			if kept[strings.ToLower(filepath.Clean(path))] {
				continue
			}
			delete(config.Indexed, path)
			removeFolderManifest(s.eng.dataDir, s.eng.Home(), path)
			removeFolderIndexCacheEntries(&cache, path)
			cacheChanged = true
		}
		if previousOutlook && (!config.OutlookIndex || !strings.EqualFold(previousOutlookFolder, config.OutlookFolder)) {
			removeFolderManifest(s.eng.dataDir, s.eng.Home(), "Outlook: "+previousOutlookFolder)
			removeOutlookIndexCacheEntries(&cache)
			config.OutlookIndexed = nil
			cacheChanged = true
		}
		folderIndexMu.Lock()
		err := saveFolderIndexConfig(s.eng.dataDir, config)
		folderIndexMu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cacheChanged {
			folderIndexMu.Lock()
			err = saveFolderIndexCache(s.eng.dataDir, cache)
			folderIndexMu.Unlock()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, map[string]any{"ok": true, "config": config})
	case "scan":
		paths := uniqueCleanPaths(request.Paths)
		if len(paths) == 0 {
			paths = config.SelectedPaths
		}
		results := make([]folderScanResult, 0, len(paths))
		for _, path := range paths {
			results = append(results, scanIndexedFolder(path))
		}
		writeJSON(w, map[string]any{"results": results})
	case "index":
		paths := uniqueCleanPaths(request.Paths)
		if len(paths) == 0 {
			paths = config.SelectedPaths
		}
		if len(paths) == 0 && !config.OutlookIndex {
			http.Error(w, "select at least one folder or enable Outlook", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(request.VisionModel) != "" {
			config.VisionModel = strings.TrimSpace(request.VisionModel)
			config.VisionProvider = strings.TrimSpace(request.VisionProvider)
		}
		if strings.TrimSpace(config.VisionModel) == "" {
			http.Error(w, "select an AI model for indexing", http.StatusBadRequest)
			return
		}
		config.VisualIndex = true
		if request.Background {
			job, started := s.startFolderIndexJob(paths, config)
			writeJSON(w, map[string]any{"ok": true, "started": started, "job": job})
			return
		}
		results, indexedAt, err := s.indexFolderPaths(r.Context(), paths, config, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "indexed_at": indexedAt, "results": results})
	default:
		http.Error(w, "unknown folder indexing action", http.StatusBadRequest)
	}
}
