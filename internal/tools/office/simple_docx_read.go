package office

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadSimpleDocxMarkdown extracts a compact Markdown-ish representation from a
// DOCX file. It is intentionally dependency-free and is used by lightweight UI
// document conversion where invoking an AI model would be wasteful.
func ReadSimpleDocxMarkdown(path string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("docx is too large: %d bytes", info.Size())
	}
	body, err := readZipEntry(path, docxDocumentEntry, maxBytes)
	if err != nil {
		return "", err
	}
	paragraphs, err := collectDocxParagraphLocations(body)
	if err != nil {
		return "", err
	}
	if len(paragraphs) == 0 {
		tool := NewReadDocx(filepath.Dir(path), maxBytes)
		return tool.renderDocument(body, 5000)
	}

	var out strings.Builder
	for _, paragraph := range paragraphs {
		text := strings.TrimSpace(paragraph.text)
		if text == "" {
			if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
				out.WriteString("\n")
			}
			continue
		}
		style := strings.ToLower(strings.TrimSpace(paragraph.style))
		switch style {
		case "heading1", "heading 1", "title":
			out.WriteString("# ")
		case "heading2", "heading 2", "subtitle":
			out.WriteString("## ")
		case "heading3", "heading 3":
			out.WriteString("### ")
		}
		out.WriteString(text)
		out.WriteString("\n\n")
		if int64(out.Len()) > 8<<20 {
			return "", fmt.Errorf("rendered docx text is too large")
		}
	}
	return strings.TrimSpace(out.String()), nil
}
