package webgui

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

const (
	folderIndexFile          = "folder-indexing.json"
	folderIndexMaxDepth      = 2
	folderIndexMaxDirEntries = 10_000
	folderIndexMaxFiles      = 5_000
	folderIndexChunkBytes    = 12 * 1024
	folderVisionMaxBytes     = 10 << 20
)

var folderIndexMu sync.Mutex

const folderLocationsCacheTTL = 5 * time.Minute

var folderLocationsCache = struct {
	sync.Mutex
	locations []folderIndexLocation
	checkedAt time.Time
}{}

type folderIndexConfig struct {
	Enabled        bool                         `json:"enabled"`
	SelectedPaths  []string                     `json:"selected_paths"`
	CustomPaths    []string                     `json:"custom_paths"`
	VisualIndex    bool                         `json:"visual_index"`
	VisionProvider string                       `json:"vision_provider,omitempty"`
	VisionModel    string                       `json:"vision_model,omitempty"`
	OutlookIndex   bool                         `json:"outlook_index,omitempty"`
	OutlookFolder  string                       `json:"outlook_folder,omitempty"`
	OutlookMax     int                          `json:"outlook_max_messages,omitempty"`
	OutlookIndexed *folderIndexedInfo           `json:"outlook_indexed,omitempty"`
	Indexed        map[string]folderIndexedInfo `json:"indexed,omitempty"`
	LastIndexedAt  string                       `json:"last_indexed_at,omitempty"`
}

type folderIndexedInfo struct {
	FileCount        int                  `json:"file_count"`
	ContentFileCount int                  `json:"content_file_count,omitempty"`
	AIFileCount      int                  `json:"ai_file_count,omitempty"`
	ReusedFileCount  int                  `json:"reused_file_count,omitempty"`
	VisualFileCount  int                  `json:"visual_file_count,omitempty"`
	SkippedFileCount int                  `json:"skipped_file_count,omitempty"`
	VisionModel      string               `json:"vision_model,omitempty"`
	Summary          string               `json:"summary,omitempty"`
	Skipped          []folderIndexSkipped `json:"skipped,omitempty"`
	IndexedAt        string               `json:"indexed_at"`
}

type folderIndexSkipped struct {
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason"`
}

type folderIndexEntry struct {
	ID       string             `json:"id"`
	Label    string             `json:"label"`
	Path     string             `json:"path"`
	Kind     string             `json:"kind"`
	Selected bool               `json:"selected"`
	Indexed  *folderIndexedInfo `json:"indexed,omitempty"`
}

type folderIndexLocation struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
}

type folderScanCounts struct {
	PDF   int `json:"pdf"`
	DOCX  int `json:"docx"`
	XLSX  int `json:"xlsx"`
	PPTX  int `json:"pptx"`
	TXT   int `json:"txt"`
	MD    int `json:"md"`
	EML   int `json:"eml"`
	MP4   int `json:"mp4"`
	PNG   int `json:"png"`
	JPG   int `json:"jpg"`
	MP3   int `json:"mp3"`
	Other int `json:"other"`
}

type folderScanResult struct {
	Path           string               `json:"path"`
	Counts         folderScanCounts     `json:"counts"`
	Total          int                  `json:"total"`
	Files          []string             `json:"files,omitempty"`
	ContentIndexed int                  `json:"content_indexed,omitempty"`
	AIIndexed      int                  `json:"ai_indexed,omitempty"`
	Reused         int                  `json:"reused,omitempty"`
	Unsupported    int                  `json:"unsupported,omitempty"`
	AnalysisFailed int                  `json:"analysis_failed,omitempty"`
	AnalysisError  string               `json:"analysis_error,omitempty"`
	VisualIndexed  int                  `json:"visual_indexed,omitempty"`
	VisualSkipped  int                  `json:"visual_skipped,omitempty"`
	VisualError    string               `json:"visual_error,omitempty"`
	FolderSummary  string               `json:"folder_summary,omitempty"`
	SkippedTotal   int                  `json:"skipped_total,omitempty"`
	Skipped        []folderIndexSkipped `json:"skipped,omitempty"`
	Error          string               `json:"error,omitempty"`
	indexPreview   map[string]string    `json:"-"`
	visualPreview  map[string]string    `json:"-"`
}

type folderIndexJob struct {
	ID          string             `json:"id"`
	State       string             `json:"state"`
	Current     int                `json:"current"`
	Total       int                `json:"total"`
	CurrentFile string             `json:"current_file,omitempty"`
	IndexedAt   string             `json:"indexed_at,omitempty"`
	StartedAt   string             `json:"started_at"`
	FinishedAt  string             `json:"finished_at,omitempty"`
	Results     []folderScanResult `json:"results,omitempty"`
	Error       string             `json:"error,omitempty"`
}

func (s *Server) folderIndexJobSnapshot() *folderIndexJob {
	s.folderJobMu.Lock()
	defer s.folderJobMu.Unlock()
	if s.folderJob == nil {
		return nil
	}
	copy := *s.folderJob
	copy.Results = append([]folderScanResult(nil), s.folderJob.Results...)
	return &copy
}

func (s *Server) startFolderIndexJob(paths []string, config folderIndexConfig) (*folderIndexJob, bool) {
	s.folderJobMu.Lock()
	if s.folderJob != nil && s.folderJob.State == "running" {
		copy := *s.folderJob
		s.folderJobMu.Unlock()
		return &copy, false
	}
	job := &folderIndexJob{
		ID: fmt.Sprintf("folder-index-%d", time.Now().UnixNano()), State: "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.folderJob = job
	ctx, cancel := context.WithCancel(context.Background())
	s.folderJobCancel = cancel
	s.folderJobMu.Unlock()

	go func() {
		results, indexedAt, err := s.indexFolderPaths(ctx, paths, config, func(current, total int, path string) {
			s.folderJobMu.Lock()
			if s.folderJob == job {
				job.Current = current
				job.Total = total
				job.CurrentFile = path
			}
			s.folderJobMu.Unlock()
		})
		s.folderJobMu.Lock()
		defer s.folderJobMu.Unlock()
		if s.folderJob != job {
			return
		}
		s.folderJobCancel = nil
		job.Results = results
		job.IndexedAt = indexedAt
		job.CurrentFile = ""
		job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if errors.Is(err, context.Canceled) {
			job.State = "canceled"
			job.Error = "analiza anulowana"
		} else if err != nil {
			job.State = "failed"
			job.Error = err.Error()
		} else {
			job.State = "completed"
			job.Current = job.Total
		}
	}()
	return s.folderIndexJobSnapshot(), true
}

func (s *Server) cancelFolderIndexJob() (*folderIndexJob, bool) {
	s.folderJobMu.Lock()
	defer s.folderJobMu.Unlock()
	if s.folderJob == nil || s.folderJob.State != "running" || s.folderJobCancel == nil {
		return s.folderJob, false
	}
	s.folderJobCancel()
	copy := *s.folderJob
	return &copy, true
}

func (s *Server) indexFolderPaths(ctx context.Context, paths []string, config folderIndexConfig, progress func(current, total int, path string)) ([]folderScanResult, string, error) {
	store, err := memory.OpenProjectStore(s.eng.dataDir, s.eng.Home())
	if err != nil {
		return nil, "", err
	}
	defer store.Close()
	folderIndexMu.Lock()
	cache := loadFolderIndexCache(s.eng.dataDir)
	folderIndexMu.Unlock()

	results := make([]folderScanResult, 0, len(paths))
	total := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return results, "", err
		}
		result := scanIndexedFolderContext(ctx, path)
		total += len(result.Files)
		results = append(results, result)
	}
	folderResultCount := len(results)
	var outlookMessages []tools.OutlookIndexMessage
	if config.OutlookIndex {
		messages, outlookErr := tools.IndexOutlookMessages(ctx, config.OutlookFolder, config.OutlookMax)
		outlookResult := folderScanResult{Path: "Outlook: " + config.OutlookFolder}
		if outlookErr != nil {
			outlookResult.Error = outlookErr.Error()
		} else {
			outlookMessages = messages
			outlookResult.Total = len(messages)
			outlookResult.Counts.Other = len(messages)
			total += len(messages)
		}
		results = append(results, outlookResult)
	}
	if progress != nil {
		progress(0, total, "")
	}
	processed := 0
	indexedAt := time.Now().UTC().Format(time.RFC3339)
	for index := 0; index < folderResultCount; index++ {
		if err := ctx.Err(); err != nil {
			folderIndexMu.Lock()
			_ = saveFolderIndexCache(s.eng.dataDir, cache)
			folderIndexMu.Unlock()
			return results, "", err
		}
		result := &results[index]
		if result.Error != "" {
			continue
		}
		base := processed
		s.addDocumentIndexPreviews(ctx, result, config, &cache, func(current, _ int, path string) {
			if progress != nil {
				progress(base+current, total, path)
			}
		})
		if err := ctx.Err(); err != nil {
			folderIndexMu.Lock()
			_ = saveFolderIndexCache(s.eng.dataDir, cache)
			folderIndexMu.Unlock()
			return results, "", err
		}
		processed += len(result.Files)
		if result.ContentIndexed == 0 && strings.TrimSpace(result.FolderSummary) == "" {
			if strings.TrimSpace(result.AnalysisError) != "" {
				result.Error = "AI nie utworzyło indeksu: " + result.AnalysisError
			} else {
				result.Error = "AI nie utworzyło żadnej notatki dla tego źródła"
			}
			continue
		}
		if result.Error == "" {
			if err := saveFolderManifest(store, *result); err != nil {
				result.Error = err.Error()
			} else {
				config.Indexed[result.Path] = folderIndexedInfo{
					FileCount: result.Total, ContentFileCount: result.ContentIndexed,
					AIFileCount: result.AIIndexed, ReusedFileCount: result.Reused,
					VisualFileCount: result.VisualIndexed, SkippedFileCount: result.SkippedTotal,
					VisionModel: config.VisionModel, Summary: result.FolderSummary,
					Skipped: append([]folderIndexSkipped(nil), result.Skipped...), IndexedAt: indexedAt,
				}
			}
		}
	}
	if config.OutlookIndex && len(results) > folderResultCount {
		result := &results[folderResultCount]
		if result.Error == "" {
			base := processed
			s.addOutlookIndexPreviews(ctx, result, outlookMessages, config, &cache, func(current, _ int, path string) {
				if progress != nil {
					progress(base+current, total, path)
				}
			})
			if err := ctx.Err(); err != nil {
				folderIndexMu.Lock()
				_ = saveFolderIndexCache(s.eng.dataDir, cache)
				folderIndexMu.Unlock()
				return results, "", err
			}
			processed += len(outlookMessages)
			if result.ContentIndexed == 0 && strings.TrimSpace(result.FolderSummary) == "" {
				if strings.TrimSpace(result.AnalysisError) != "" {
					result.Error = "AI nie utworzyło indeksu: " + result.AnalysisError
				} else {
					result.Error = "AI nie utworzyło żadnej notatki dla tego źródła"
				}
			} else if err := saveFolderManifest(store, *result); err != nil {
				result.Error = err.Error()
			} else {
				config.OutlookIndexed = &folderIndexedInfo{
					FileCount: result.Total, ContentFileCount: result.ContentIndexed,
					AIFileCount: result.AIIndexed, ReusedFileCount: result.Reused, SkippedFileCount: result.SkippedTotal,
					VisionModel: config.VisionModel, Summary: result.FolderSummary,
					Skipped: append([]folderIndexSkipped(nil), result.Skipped...), IndexedAt: indexedAt,
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		folderIndexMu.Lock()
		_ = saveFolderIndexCache(s.eng.dataDir, cache)
		folderIndexMu.Unlock()
		return results, "", err
	}
	config.SelectedPaths = paths
	config.LastIndexedAt = indexedAt
	folderIndexMu.Lock()
	err = saveFolderIndexCache(s.eng.dataDir, cache)
	if err == nil {
		err = saveFolderIndexConfig(s.eng.dataDir, config)
	}
	folderIndexMu.Unlock()
	if progress != nil {
		progress(total, total, "")
	}
	return results, indexedAt, err
}

func defaultFolderIndexConfig() folderIndexConfig {
	return folderIndexConfig{
		SelectedPaths: []string{},
		CustomPaths:   []string{},
		Indexed:       map[string]folderIndexedInfo{},
		OutlookFolder: "Inbox",
		OutlookMax:    250,
	}
}

func loadFolderIndexConfig(dataDir string) folderIndexConfig {
	config := defaultFolderIndexConfig()
	data, err := os.ReadFile(filepath.Join(dataDir, folderIndexFile))
	if err != nil {
		return config
	}
	if json.Unmarshal(data, &config) != nil {
		return defaultFolderIndexConfig()
	}
	if config.SelectedPaths == nil {
		config.SelectedPaths = []string{}
	}
	if config.CustomPaths == nil {
		config.CustomPaths = []string{}
	}
	if config.Indexed == nil {
		config.Indexed = map[string]folderIndexedInfo{}
	}
	if strings.TrimSpace(config.OutlookFolder) == "" {
		config.OutlookFolder = "Inbox"
	}
	if config.OutlookMax <= 0 {
		config.OutlookMax = 250
	}
	return config
}

func saveFolderIndexConfig(dataDir string, config folderIndexConfig) error {
	config.SelectedPaths = uniqueCleanPaths(config.SelectedPaths)
	config.CustomPaths = uniqueCleanPaths(config.CustomPaths)
	config.Enabled = len(config.SelectedPaths) > 0 || config.OutlookIndex
	config.VisionProvider = strings.TrimSpace(config.VisionProvider)
	config.VisionModel = strings.TrimSpace(config.VisionModel)
	config.OutlookFolder = strings.TrimSpace(config.OutlookFolder)
	if config.OutlookFolder == "" {
		config.OutlookFolder = "Inbox"
	}
	if config.OutlookMax <= 0 {
		config.OutlookMax = 250
	}
	if config.OutlookMax > 2000 {
		config.OutlookMax = 2000
	}
	if config.Indexed == nil {
		config.Indexed = map[string]folderIndexedInfo{}
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, folderIndexFile), data, 0o644)
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.Clean(strings.TrimSpace(raw))
		if path == "." || !filepath.IsAbs(path) {
			continue
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, path)
	}
	return out
}

func folderIndexLocations() []folderIndexLocation {
	folderLocationsCache.Lock()
	if len(folderLocationsCache.locations) > 0 && time.Since(folderLocationsCache.checkedAt) < folderLocationsCacheTTL {
		locations := append([]folderIndexLocation(nil), folderLocationsCache.locations...)
		folderLocationsCache.Unlock()
		return locations
	}
	folderLocationsCache.Unlock()

	locations := []folderIndexLocation{}
	add := func(label, path, kind string) {
		path = filepath.Clean(path)
		if path == "." || !filepath.IsAbs(path) {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		locations = append(locations, folderIndexLocation{Label: label, Path: path, Kind: kind})
	}
	for _, location := range suggestedUserFolders() {
		add(location.Label, location.Path, location.Kind)
	}
	for _, root := range logicalDriveRoots() {
		add("Dysk "+strings.TrimSuffix(root, `\`), root, "suggested")
	}
	folderLocationsCache.Lock()
	folderLocationsCache.locations = append([]folderIndexLocation(nil), locations...)
	folderLocationsCache.checkedAt = time.Now()
	folderLocationsCache.Unlock()
	return append([]folderIndexLocation(nil), locations...)
}

func folderIndexEntries(config folderIndexConfig) []folderIndexEntry {
	selected := map[string]bool{}
	for _, path := range config.SelectedPaths {
		selected[strings.ToLower(filepath.Clean(path))] = true
	}
	locations := folderIndexLocations()
	locationByPath := map[string]folderIndexLocation{}
	for _, location := range locations {
		locationByPath[strings.ToLower(filepath.Clean(location.Path))] = location
	}
	entries := []folderIndexEntry{}
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if path == "." || !filepath.IsAbs(path) {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		label := filepath.Base(path)
		if label == "." || label == string(filepath.Separator) {
			label = path
		}
		kind := "saved"
		if location, ok := locationByPath[key]; ok {
			label = location.Label
			kind = location.Kind
		}
		idHash := sha1.Sum([]byte(key))
		entry := folderIndexEntry{
			ID:       "folder-" + hex.EncodeToString(idHash[:])[:10],
			Label:    label,
			Path:     path,
			Kind:     kind,
			Selected: selected[key],
		}
		if indexed, ok := config.Indexed[path]; ok {
			copy := indexed
			entry.Indexed = &copy
		}
		entries = append(entries, entry)
	}

	for _, path := range config.CustomPaths {
		add(path)
	}
	for _, path := range config.SelectedPaths {
		add(path)
	}
	return entries
}

func folderIndexSuggestions(config folderIndexConfig) []folderIndexLocation {
	configured := map[string]bool{}
	for _, path := range append(append([]string{}, config.CustomPaths...), config.SelectedPaths...) {
		configured[strings.ToLower(filepath.Clean(path))] = true
	}
	suggestions := []folderIndexLocation{}
	for _, location := range folderIndexLocations() {
		if !configured[strings.ToLower(filepath.Clean(location.Path))] {
			suggestions = append(suggestions, location)
		}
	}
	return suggestions
}

func removeFolderManifest(dataDir, home, path string) {
	store, err := memory.OpenProjectStore(dataDir, home)
	if err != nil {
		return
	}
	defer store.Close()
	if entries, err := store.ByTag(folderIndexTag(path), 200); err == nil {
		for _, entry := range entries {
			_ = store.Delete(entry.ID)
		}
	}
}

func scanIndexedFolder(root string) folderScanResult {
	return scanIndexedFolderContext(context.Background(), root)
}

func scanIndexedFolderContext(ctx context.Context, root string) folderScanResult {
	result := folderScanResult{Path: root, Files: []string{}}
	info, err := os.Stat(root)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if !info.IsDir() {
		result.Error = "ścieżka nie jest folderem"
		return result
	}
	var walk func(string, int)
	walk = func(dir string, depth int) {
		if result.Error != "" || len(result.Files) >= folderIndexMaxFiles || ctx.Err() != nil {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		if len(entries) > folderIndexMaxDirEntries {
			entries = entries[:folderIndexMaxDirEntries]
		}
		for _, entry := range entries {
			if len(result.Files) >= folderIndexMaxFiles || ctx.Err() != nil {
				return
			}
			path := filepath.Join(dir, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				if depth < folderIndexMaxDepth {
					walk(path, depth+1)
				}
				continue
			}
			result.Files = append(result.Files, path)
			countFolderExtension(&result.Counts, strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), ".")))
		}
	}
	walk(root, 0)
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
	}
	result.Total = len(result.Files)
	return result
}

func countFolderExtension(counts *folderScanCounts, extension string) {
	switch extension {
	case "pdf":
		counts.PDF++
	case "doc", "docx":
		counts.DOCX++
	case "xls", "xlsx":
		counts.XLSX++
	case "ppt", "pptx":
		counts.PPTX++
	case "txt":
		counts.TXT++
	case "md", "markdown":
		counts.MD++
	case "eml":
		counts.EML++
	case "mp4", "avi", "mkv", "mov", "webm":
		counts.MP4++
	case "png":
		counts.PNG++
	case "jpg", "jpeg":
		counts.JPG++
	case "mp3", "wav", "flac", "ogg":
		counts.MP3++
	default:
		counts.Other++
	}
}

func isVisualIndexImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func (s *Server) addVisualIndexPreviews(ctx context.Context, result *folderScanResult, config folderIndexConfig) {
	if result == nil || !config.VisualIndex {
		return
	}
	if strings.TrimSpace(config.VisionModel) == "" {
		result.VisualError = "wybierz model do analizy obrazów"
		return
	}
	provider, err := s.eng.providerForSelection(config.VisionModel, config.VisionProvider, "vision-index")
	if err != nil {
		result.VisualError = err.Error()
		return
	}
	result.visualPreview = map[string]string{}
	imageCount := 0
	for _, path := range result.Files {
		if !isVisualIndexImage(path) {
			continue
		}
		imageCount++
		caption, captionErr := describeIndexedImage(ctx, provider, path)
		if captionErr != nil {
			if result.VisualError == "" {
				result.VisualError = captionErr.Error()
			}
			continue
		}
		result.visualPreview[path] = caption
		result.VisualIndexed++
	}
	result.VisualSkipped = imageCount - result.VisualIndexed
}

func describeIndexedImage(ctx context.Context, provider llm.Provider, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > folderVisionMaxBytes {
		return "", fmt.Errorf("obraz %s jest zbyt duży do analizy", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mediaType := http.DetectContentType(data)
	if !allowedVisionMIME(mediaType) {
		return "", fmt.Errorf("nieobsługiwany format obrazu %s", filepath.Base(path))
	}
	prompt := "Opisz zawartość tego obrazu po polsku w maksymalnie 80 słowach. Przepisz istotny widoczny tekst. Nie zgaduj danych, których nie widać. Zwróć wyłącznie opis do indeksu wyszukiwania."
	message := llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: prompt},
		{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: base64.StdEncoding.EncodeToString(data), MediaType: mediaType}},
	}}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	stream, err := provider.Complete(llm.WithPurpose(callCtx, "vision-index"), []llm.Message{message}, nil)
	if err != nil {
		return "", err
	}
	var caption strings.Builder
	for delta := range stream {
		if delta.Err != nil {
			return "", delta.Err
		}
		caption.WriteString(delta.Content)
	}
	text := strings.Join(strings.Fields(strings.TrimSpace(memory.StripReasoning(caption.String()))), " ")
	if text == "" {
		return "", fmt.Errorf("model nie zwrócił opisu obrazu %s", filepath.Base(path))
	}
	if len([]rune(text)) > 700 {
		text = string([]rune(text)[:700]) + "…"
	}
	return text, nil
}

func folderIndexTag(path string) string {
	hash := sha1.Sum([]byte(strings.ToLower(filepath.Clean(path))))
	return "folder-" + hex.EncodeToString(hash[:])[:12]
}

func saveFolderManifest(store *memory.Store, scan folderScanResult) error {
	tag := folderIndexTag(scan.Path)
	if old, err := store.ByTag(tag, 200); err == nil {
		for _, entry := range old {
			_ = store.Delete(entry.ID)
		}
	}
	lines := []string{
		fmt.Sprintf("Indeks folderu: %s", scan.Path),
		fmt.Sprintf("Liczba plików: %d. Zakres skanu: maksymalnie %d poziomy.", scan.Total, folderIndexMaxDepth),
	}
	if scan.FolderSummary != "" {
		lines = append(lines, "Notatka AI o folderze: "+scan.FolderSummary)
	}
	for _, path := range scan.Files {
		relative, err := filepath.Rel(scan.Path, path)
		if err != nil {
			relative = path
		}
		line := "- " + relative
		preview := ""
		if scan.indexPreview != nil {
			preview = scan.indexPreview[path]
		}
		if preview == "" && scan.visualPreview != nil {
			preview = scan.visualPreview[path]
		}
		// The scan only inventories files. Searchable entries are created from
		// AI-produced notes; a local text fallback would silently index without
		// the model and violate the explicit NestCafe workflow.
		if preview == "" {
			continue
		}
		line += " — " + preview
		lines = append(lines, line)
	}
	chunk := strings.Builder{}
	chunkIndex := 0
	flush := func() error {
		if chunk.Len() == 0 {
			return nil
		}
		id := fmt.Sprintf("folder-index-%s-%03d", strings.TrimPrefix(tag, "folder-"), chunkIndex)
		err := store.Put(memory.Entry{
			ID:      id,
			Scope:   memory.ScopeFact,
			Content: chunk.String(),
			Tags:    []string{"folder-index", tag},
			Source:  memory.SourceTool,
		})
		chunk.Reset()
		chunkIndex++
		return err
	}
	for _, line := range lines {
		if chunk.Len()+len(line)+1 > folderIndexChunkBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		chunk.WriteString(line)
		chunk.WriteByte('\n')
	}
	return flush()
}

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
			http.Error(w, "indeksowanie jest w toku; ustawienia źródeł można zmienić po jego zakończeniu", http.StatusConflict)
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
			http.Error(w, "wybierz model AI do indeksowania", http.StatusBadRequest)
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
