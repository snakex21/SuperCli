package webgui

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/office"
	"supercli/internal/tools/sandbox"
)

func (s *Server) handleAttachmentDocumentPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	full, err := sandbox.ResolveSafe(s.eng.Home(), raw)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxChatAttachmentBytes {
		http.Error(w, "document preview is unavailable", http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(info.Name()))
	var text string
	switch ext {
	case ".docx":
		text, err = office.ReadSimpleDocxMarkdown(full, maxChatAttachmentBytes)
	case ".md", ".markdown", ".txt", ".csv", ".json", ".yaml", ".yml", ".xml", ".html", ".css", ".js", ".ts", ".tsx", ".go", ".py":
		file, openErr := os.Open(full)
		if openErr != nil {
			err = openErr
			break
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxChatAttachmentBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			err = readErr
		} else if closeErr != nil {
			err = closeErr
		} else if int64(len(data)) > maxChatAttachmentBytes {
			http.Error(w, "document preview is too large", http.StatusBadRequest)
			return
		} else {
			text = strings.TrimPrefix(string(data), "\ufeff")
		}
	default:
		http.Error(w, "document preview supports DOCX and text files", http.StatusUnsupportedMediaType)
		return
	}
	if err != nil {
		http.Error(w, "read document preview: "+err.Error(), http.StatusBadRequest)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		http.Error(w, "document contains no readable text", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"ok":   true,
		"name": info.Name(),
		"text": text,
	})
}
