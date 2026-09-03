package webgui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
)

func summarizeIndexedDocument(ctx context.Context, provider llm.Provider, path, kind, text, language string) (string, error) {
	input := limitUTF8Bytes(text, folderIndexAIInputBytes)
	prompt := fmt.Sprintf("Write a concise search-index entry for the file %q (type: %s). State the subject and the important people, organisations, dates and keywords. At most 100 words. Do not add anything that is not present in the text. Return the index entry only. %s\n\nTEXT:\n%s", filepath.Base(path), kind, respondInLanguage(language), input)
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

func summarizeIndexedFolder(ctx context.Context, provider llm.Provider, result *folderScanResult, language string) (string, error) {
	if provider == nil || result == nil {
		return "", fmt.Errorf("no AI model available to describe the folder")
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
			fmt.Fprintf(&notes, "- %s (skipped: %s)\n", relative, skipped.Reason)
			if notes.Len() >= folderIndexAIInputBytes {
				break
			}
		}
	}
	prompt := fmt.Sprintf("Write a short note describing the folder %q, based on the file names and on the notes the AI has already produced. Say what the folder is probably for, which topics it covers and what kinds of material it holds. Do not guess beyond the given data. At most 120 words. Return the folder note only. %s\n\nFILES AND NOTES:\n%s", result.Path, respondInLanguage(language), limitUTF8Bytes(notes.String(), folderIndexAIInputBytes))
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
		return "", fmt.Errorf("the model returned no folder note")
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
