package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	folderIndexCacheFile       = "folder-index-cache.json"
	folderIndexCacheVersion    = 2
	folderIndexExtractMaxBytes = 128 * 1024
	folderIndexAIInputBytes    = 24 * 1024
	folderIndexPreviewBytes    = 3 * 1024
)

type folderIndexCache struct {
	Version int                             `json:"version"`
	Files   map[string]folderIndexCacheItem `json:"files"`
}

type folderIndexCacheItem struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedNS int64  `json:"modified_ns"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	Preview    string `json:"preview"`
	AI         bool   `json:"ai"`
	Visual     bool   `json:"visual"`
}

func defaultFolderIndexCache() folderIndexCache {
	return folderIndexCache{Version: folderIndexCacheVersion, Files: map[string]folderIndexCacheItem{}}
}

func loadFolderIndexCache(dataDir string) folderIndexCache {
	cache := defaultFolderIndexCache()
	data, err := os.ReadFile(filepath.Join(dataDir, folderIndexCacheFile))
	if err != nil || json.Unmarshal(data, &cache) != nil || cache.Version != folderIndexCacheVersion {
		return defaultFolderIndexCache()
	}
	if cache.Files == nil {
		cache.Files = map[string]folderIndexCacheItem{}
	}
	return cache
}

func saveFolderIndexCache(dataDir string, cache folderIndexCache) error {
	cache.Version = folderIndexCacheVersion
	if cache.Files == nil {
		cache.Files = map[string]folderIndexCacheItem{}
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dataDir, folderIndexCacheFile)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, target)
}

func folderCacheKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func folderIndexModelKey(config folderIndexConfig) string {
	return strings.ToLower(strings.TrimSpace(config.VisionProvider) + "/" + strings.TrimSpace(config.VisionModel))
}

func removeFolderIndexCacheEntries(cache *folderIndexCache, root string) {
	if cache == nil {
		return
	}
	for key, item := range cache.Files {
		if pathWithinFolder(item.Path, root) {
			delete(cache.Files, key)
		}
	}
}

func pruneFolderIndexCache(cache *folderIndexCache, root string, files []string) {
	if cache == nil {
		return
	}
	current := make(map[string]bool, len(files))
	for _, path := range files {
		current[folderCacheKey(path)] = true
	}
	for key, item := range cache.Files {
		if pathWithinFolder(item.Path, root) && !current[key] {
			delete(cache.Files, key)
		}
	}
}

func pathWithinFolder(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." {
		return relative == "."
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (s *Server) addDocumentIndexPreviews(ctx context.Context, result *folderScanResult, config folderIndexConfig, cache *folderIndexCache, progress func(current, total int, path string)) {
	if result == nil || cache == nil {
		return
	}
	result.indexPreview = map[string]string{}
	modelKey := folderIndexModelKey(config)
	if strings.TrimSpace(config.VisionModel) == "" {
		result.AnalysisError = "wybierz model AI do indeksowania"
		return
	}
	provider, err := s.eng.providerForSelection(config.VisionModel, config.VisionProvider, "document-index")
	if err != nil {
		result.AnalysisError = err.Error()
		return
	}
	pruneFolderIndexCache(cache, result.Path, result.Files)
	for index, path := range result.Files {
		if progress != nil {
			progress(index, len(result.Files), path)
		}
		if err := ctx.Err(); err != nil {
			result.Error = err.Error()
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			result.Unsupported++
			appendFolderIndexSkipped(result, path, "plik", "nie można odczytać pliku")
			continue
		}
		key := folderCacheKey(path)
		if cached, ok := cache.Files[key]; ok && cached.Size == info.Size() && cached.ModifiedNS == info.ModTime().UnixNano() && cached.Model == modelKey {
			if cached.Preview != "" {
				result.indexPreview[path] = cached.Preview
				result.ContentIndexed++
			}
			if cached.AI {
				result.AIIndexed++
			}
			if cached.Visual {
				result.VisualIndexed++
			}
			result.Reused++
			continue
		}

		item := folderIndexCacheItem{
			Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Model: modelKey,
		}
		if isVisualIndexImage(path) {
			item.Kind = "image"
			caption, captionErr := describeIndexedImage(ctx, provider, path)
			if captionErr != nil {
				result.AnalysisFailed++
				result.VisualSkipped++
				appendFolderAnalysisError(result, captionErr)
				appendFolderIndexSkipped(result, path, "obraz", captionErr.Error())
				continue
			}
			item.Preview = "Obraz: " + caption
			item.AI = true
			item.Visual = true
			result.indexPreview[path] = item.Preview
			result.ContentIndexed++
			result.AIIndexed++
			result.VisualIndexed++
			cache.Files[key] = item
			continue
		}

		text, kind, supported, extractErr := extractFolderIndexText(ctx, path)
		item.Kind = kind
		if extractErr != nil {
			result.AnalysisFailed++
			appendFolderAnalysisError(result, extractErr)
			appendFolderIndexSkipped(result, path, kind, extractErr.Error())
			continue
		}
		if !supported {
			result.Unsupported++
			appendFolderIndexSkipped(result, path, kind, "format nie jest obsługiwany przez indeksowanie AI")
			continue
		}
		text = normalizeIndexText(text)
		if text == "" {
			result.Unsupported++
			appendFolderIndexSkipped(result, path, kind, "nie udało się wydobyć tekstu dla modelu")
			continue
		}

		summary, summaryErr := summarizeIndexedDocument(ctx, provider, path, kind, text)
		if summaryErr != nil || strings.TrimSpace(summary) == "" {
			result.AnalysisFailed++
			if summaryErr == nil {
				summaryErr = fmt.Errorf("model nie przygotował notatki dla %s", filepath.Base(path))
			}
			appendFolderAnalysisError(result, summaryErr)
			appendFolderIndexSkipped(result, path, kind, summaryErr.Error())
			continue
		}
		item.AI = true
		result.AIIndexed++
		// Exact text is used only as input to the selected model. The searchable
		// index receives the model's note, never a local extraction fallback.
		item.Preview = buildDocumentIndexPreview(kind, summary, "")
		if item.Preview != "" {
			result.indexPreview[path] = item.Preview
			result.ContentIndexed++
		}
		cache.Files[key] = item
	}
	if progress != nil {
		progress(len(result.Files), len(result.Files), "")
	}
	if folderSummary, summaryErr := summarizeIndexedFolder(ctx, provider, result); summaryErr != nil {
		appendFolderAnalysisError(result, summaryErr)
	} else {
		result.FolderSummary = folderSummary
	}
}

func appendFolderIndexSkipped(result *folderScanResult, path, kind, reason string) {
	if result == nil {
		return
	}
	result.SkippedTotal++
	// Keep the report useful without allowing one huge folder to create a
	// multi-megabyte settings response. The total remains exact.
	if len(result.Skipped) >= 200 {
		return
	}
	result.Skipped = append(result.Skipped, folderIndexSkipped{
		Path: path, Kind: strings.TrimSpace(kind), Reason: strings.TrimSpace(reason),
	})
}

func appendFolderAnalysisError(result *folderScanResult, err error) {
	if result == nil || err == nil || result.AnalysisError != "" {
		return
	}
	result.AnalysisError = err.Error()
	result.VisualError = result.AnalysisError
}

func countVisualIndexImages(paths []string) int {
	count := 0
	for _, path := range paths {
		if isVisualIndexImage(path) {
			count++
		}
	}
	return count
}
