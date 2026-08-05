package webgui

import (
	"context"
	"strings"
	"time"

	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

func (s *Server) indexFolderPaths(ctx context.Context, paths []string, config folderIndexConfig, progress func(current, total int, path string)) ([]folderScanResult, string, error) {
	// Indexing is entirely out-of-turn model work (document summaries,
	// folder notes, image captions); count it so its cost is visible.
	ctx = s.eng.countOffTurnCalls(ctx)
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
