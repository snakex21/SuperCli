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
	"unicode"
	"unicode/utf8"

	"supercli/internal/tools"
)

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
