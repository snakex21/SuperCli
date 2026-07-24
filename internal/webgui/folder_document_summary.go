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
