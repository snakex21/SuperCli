package webgui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"supercli/internal/tools"
)

func outlookCacheKey(entryID string) string {
	return "outlook:" + strings.ToLower(strings.TrimSpace(entryID))
}

func removeOutlookIndexCacheEntries(cache *folderIndexCache) {
	if cache == nil {
		return
	}
	for key := range cache.Files {
		if strings.HasPrefix(key, "outlook:") {
			delete(cache.Files, key)
		}
	}
}

func pruneOutlookIndexCache(cache *folderIndexCache, messages []tools.OutlookIndexMessage) {
	current := make(map[string]bool, len(messages))
	for _, message := range messages {
		current[outlookCacheKey(message.EntryID)] = true
	}
	for key := range cache.Files {
		if strings.HasPrefix(key, "outlook:") && !current[key] {
			delete(cache.Files, key)
		}
	}
}

func (s *Server) addOutlookIndexPreviews(ctx context.Context, result *folderScanResult, messages []tools.OutlookIndexMessage, config folderIndexConfig, cache *folderIndexCache, progress func(current, total int, path string)) {
	result.indexPreview = map[string]string{}
	modelKey := folderIndexModelKey(config)
	if strings.TrimSpace(config.VisionModel) == "" {
		result.AnalysisError = "wybierz model AI do indeksowania"
		return
	}
	provider, err := s.eng.providerForSelection(config.VisionModel, config.VisionProvider, "outlook-index")
	if err != nil {
		result.AnalysisError = err.Error()
		return
	}
	pruneOutlookIndexCache(cache, messages)
	for index, message := range messages {
		virtualPath := fmt.Sprintf("outlook://%s/%s", strings.TrimSpace(config.OutlookFolder), message.EntryID)
		result.Files = append(result.Files, virtualPath)
		if progress != nil {
			progress(index, len(messages), "Outlook · "+message.Subject)
		}
		modified := message.ModifiedAt
		if modified.IsZero() {
			modified = message.ReceivedAt
		}
		key := outlookCacheKey(message.EntryID)
		if cached, ok := cache.Files[key]; ok && cached.ModifiedNS == modified.UnixNano() && cached.Model == modelKey {
			result.indexPreview[virtualPath] = cached.Preview
			result.ContentIndexed++
			if cached.AI {
				result.AIIndexed++
			}
			result.Reused++
			continue
		}
		exact := buildOutlookExactText(message)
		if strings.TrimSpace(exact) == "" {
			result.Unsupported++
			appendFolderIndexSkipped(result, virtualPath, "Outlook", "wiadomość nie zawiera tekstu dla modelu")
			continue
		}
		summary, summaryErr := summarizeIndexedDocument(ctx, provider, filepath.Base(virtualPath), "wiadomość Outlook", exact)
		if summaryErr != nil || strings.TrimSpace(summary) == "" {
			result.AnalysisFailed++
			if summaryErr == nil {
				summaryErr = fmt.Errorf("model nie przygotował notatki dla wiadomości %s", message.Subject)
			}
			appendFolderAnalysisError(result, summaryErr)
			appendFolderIndexSkipped(result, virtualPath, "Outlook", summaryErr.Error())
			continue
		}
		result.AIIndexed++
		preview := buildDocumentIndexPreview("Outlook", summary, "")
		result.indexPreview[virtualPath] = preview
		result.ContentIndexed++
		cache.Files[key] = folderIndexCacheItem{
			Path: virtualPath, ModifiedNS: modified.UnixNano(), Model: modelKey,
			Kind: "Outlook", Preview: preview, AI: true,
		}
	}
	if progress != nil {
		progress(len(messages), len(messages), "")
	}
	if folderSummary, summaryErr := summarizeIndexedFolder(ctx, provider, result); summaryErr != nil {
		appendFolderAnalysisError(result, summaryErr)
	} else {
		result.FolderSummary = folderSummary
	}
}

func buildOutlookExactText(message tools.OutlookIndexMessage) string {
	parts := []string{
		"Temat: " + message.Subject,
		"Nadawca: " + strings.TrimSpace(message.Sender+" "+message.SenderAddress),
		"Do: " + message.To,
		"DW: " + message.CC,
	}
	if !message.ReceivedAt.IsZero() {
		parts = append(parts, "Data: "+message.ReceivedAt.Format("2006-01-02 15:04"))
	}
	if len(message.AttachmentNames) > 0 {
		parts = append(parts, "Załączniki: "+strings.Join(message.AttachmentNames, ", "))
	}
	parts = append(parts, "Treść: "+message.Body)
	return normalizeIndexText(strings.Join(parts, "\n"))
}
