package webgui

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
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

func extractFolderIndexText(ctx context.Context, path string) (string, string, bool, error) {
	extension := strings.ToLower(filepath.Ext(path))
	base := filepath.Dir(path)
	args, _ := json.Marshal(map[string]any{"path": path})
	switch extension {
	case ".pdf":
		reader := tools.NewReadPdf(base, 0)
		reader.MaxPages = 200
		reader.MaxOutputBytes = folderIndexExtractMaxBytes
		result, err := reader.Execute(ctx, args)
		return toolIndexText(result.Text, "PDF", true, result.Err, err)
	case ".docx":
		reader := tools.NewReadDocx(base, 0)
		reader.MaxOutputBytes = folderIndexExtractMaxBytes
		result, err := reader.Execute(ctx, args)
		return toolIndexText(result.Text, "Word", true, result.Err, err)
	case ".xlsx":
		reader := tools.NewReadXlsx(base, 0)
		reader.MaxCells = 20_000
		reader.MaxOutputBytes = folderIndexExtractMaxBytes
		result, err := reader.Execute(ctx, args)
		return toolIndexText(result.Text, "Excel", true, result.Err, err)
	case ".pptx":
		text, err := extractPPTXText(path, folderIndexExtractMaxBytes)
		return text, "PowerPoint", true, err
	case ".txt", ".md", ".markdown", ".json", ".jsonl", ".csv", ".xml", ".html", ".htm",
		".js", ".jsx", ".ts", ".tsx", ".py", ".go", ".rs", ".java", ".cs", ".css", ".scss",
		".yml", ".yaml", ".toml", ".ini", ".sql", ".ps1", ".sh", ".log", ".rtf", ".eml":
		text, err := readIndexTextFile(path, folderIndexExtractMaxBytes)
		return text, strings.TrimPrefix(strings.ToUpper(extension), "."), true, err
	default:
		return "", strings.TrimPrefix(strings.ToUpper(extension), "."), false, nil
	}
}

func toolIndexText(text, kind string, supported bool, toolErr, callErr error) (string, string, bool, error) {
	if callErr != nil {
		return "", kind, supported, callErr
	}
	if toolErr != nil {
		return "", kind, supported, toolErr
	}
	return text, kind, supported, nil
}

func readIndexTextFile(path string, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		data = data[:maxBytes]
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("plik %s nie jest tekstem UTF-8", filepath.Base(path))
	}
	return string(data), nil
}

func extractPPTXText(path string, maxBytes int64) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("otwórz PowerPoint: %w", err)
	}
	defer archive.Close()
	var slides []*zip.File
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			slides = append(slides, file)
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].Name < slides[j].Name })
	var out strings.Builder
	for index, slide := range slides {
		if slide.UncompressedSize64 > uint64(maxBytes) {
			continue
		}
		reader, err := slide.Open()
		if err != nil {
			continue
		}
		decoder := xml.NewDecoder(io.LimitReader(reader, maxBytes))
		var pieces []string
		for {
			token, decodeErr := decoder.Token()
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				break
			}
			start, ok := token.(xml.StartElement)
			if !ok || start.Name.Local != "t" {
				continue
			}
			var text string
			if decoder.DecodeElement(&text, &start) == nil && strings.TrimSpace(text) != "" {
				pieces = append(pieces, strings.TrimSpace(text))
			}
		}
		reader.Close()
		if len(pieces) == 0 {
			continue
		}
		fmt.Fprintf(&out, "Slajd %d: %s\n", index+1, strings.Join(pieces, " | "))
		if int64(out.Len()) >= maxBytes {
			break
		}
	}
	text := out.String()
	if int64(len(text)) > maxBytes {
		text = text[:maxBytes]
	}
	return text, nil
}

func normalizeIndexText(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}

func summarizeIndexedDocument(ctx context.Context, provider llm.Provider, path, kind, text string) (string, error) {
	input := limitUTF8Bytes(text, folderIndexAIInputBytes)
	prompt := fmt.Sprintf("Przygotuj zwięzły wpis do indeksu wyszukiwania dla pliku %q (typ: %s). Podaj po polsku temat, ważne osoby, organizacje, daty i słowa kluczowe. Maksymalnie 100 słów. Nie dodawaj informacji, których nie ma w tekście. Zwróć wyłącznie wpis indeksu.\n\nTEKST:\n%s", filepath.Base(path), kind, input)
	callCtx, cancel := context.WithTimeout(llm.WithBackground(llm.WithPurpose(ctx, "document-index")), 90*time.Second)
	defer cancel()
	stream, err := provider.Complete(callCtx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for delta := range stream {
		if delta.Err != nil {
			return "", delta.Err
		}
		out.WriteString(delta.Content)
	}
	return limitUTF8Bytes(normalizeIndexText(memory.StripReasoning(out.String())), 1200), nil
}

func summarizeIndexedFolder(ctx context.Context, provider llm.Provider, result *folderScanResult) (string, error) {
	if provider == nil || result == nil {
		return "", fmt.Errorf("brak modelu AI do opisania folderu")
	}
	var notes strings.Builder
	for _, path := range result.Files {
		preview := result.indexPreview[path]
		if preview == "" {
			continue
		}
		relative, err := filepath.Rel(result.Path, path)
		if err != nil {
			relative = filepath.Base(path)
		}
		fmt.Fprintf(&notes, "- %s — %s\n", relative, preview)
		if notes.Len() >= folderIndexAIInputBytes {
			break
		}
	}
	if notes.Len() == 0 {
		for _, skipped := range result.Skipped {
			relative, err := filepath.Rel(result.Path, skipped.Path)
			if err != nil {
				relative = filepath.Base(skipped.Path)
			}
			fmt.Fprintf(&notes, "- %s (pominięty: %s)\n", relative, skipped.Reason)
			if notes.Len() >= folderIndexAIInputBytes {
				break
			}
		}
	}
	prompt := fmt.Sprintf("Przygotuj po polsku krótką notatkę opisującą folder %q na podstawie nazw plików i notatek, które już utworzyło AI. Napisz, do czego folder prawdopodobnie służy, jakie zawiera tematy i jakie typy materiałów. Nie zgaduj ponad podane dane. Maksymalnie 120 słów. Zwróć wyłącznie notatkę o folderze.\n\nPLIKI I NOTATKI:\n%s", result.Path, limitUTF8Bytes(notes.String(), folderIndexAIInputBytes))
	callCtx, cancel := context.WithTimeout(llm.WithBackground(llm.WithPurpose(ctx, "folder-index-summary")), 90*time.Second)
	defer cancel()
	stream, err := provider.Complete(callCtx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for delta := range stream {
		if delta.Err != nil {
			return "", delta.Err
		}
		out.WriteString(delta.Content)
	}
	text := normalizeIndexText(memory.StripReasoning(out.String()))
	if text == "" {
		return "", fmt.Errorf("model nie przygotował notatki o folderze")
	}
	return limitUTF8Bytes(text, 1600), nil
}

func buildDocumentIndexPreview(kind, summary, text string) string {
	var parts []string
	if kind != "" {
		parts = append(parts, "Typ: "+kind+".")
	}
	if summary != "" {
		parts = append(parts, "Analiza AI: "+summary)
	}
	if text != "" {
		parts = append(parts, "Treść: "+limitUTF8Bytes(text, 1900))
	}
	return limitUTF8Bytes(strings.Join(parts, " "), folderIndexPreviewBytes)
}

func limitUTF8Bytes(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	text = text[:max]
	for !utf8.ValidString(text) && len(text) > 0 {
		text = text[:len(text)-1]
	}
	return strings.TrimSpace(text) + "…"
}
