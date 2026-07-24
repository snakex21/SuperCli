package webgui

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
)

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
