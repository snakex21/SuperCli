package webgui

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/office"
)

const maxLocalDocumentImportBytes = 32 << 20

// handleDocumentImport extracts text from an existing DOCX/Markdown/TXT file
// locally. It deliberately does not invoke the model; the OCR/document module
// can then export the same result to any supported format.
func (s *Server) handleDocumentImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLocalDocumentImportBytes+(1<<20))
	upload, cleanup, err := stageMultipartUpload(r, s.eng.DataDir(), "document-import-*", maxLocalDocumentImportBytes, "file", "document", "files")
	if err != nil {
		http.Error(w, "invalid document upload: "+err.Error(), uploadErrorStatus(err))
		return
	}
	defer cleanup()
	if upload.Size <= 0 {
		http.Error(w, "document is empty or too large", http.StatusBadRequest)
		return
	}
	ext := strings.ToLower(filepath.Ext(upload.Name))
	if ext != ".docx" && ext != ".md" && ext != ".markdown" && ext != ".txt" {
		http.Error(w, "supported document formats: DOCX, MD, TXT", http.StatusUnsupportedMediaType)
		return
	}

	var text string
	switch ext {
	case ".docx":
		text, err = office.ReadSimpleDocxMarkdown(upload.Path, maxLocalDocumentImportBytes)
		if err != nil {
			http.Error(w, "read docx: "+err.Error(), http.StatusBadRequest)
			return
		}
	default:
		file, err := os.Open(upload.Path)
		if err != nil {
			http.Error(w, "open document: "+err.Error(), http.StatusInternalServerError)
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxLocalDocumentImportBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > maxLocalDocumentImportBytes {
			http.Error(w, "read text document", http.StatusBadRequest)
			return
		}
		text = strings.TrimPrefix(string(data), "\ufeff")
	}

	text = strings.TrimSpace(text)
	if text == "" {
		http.Error(w, "document contains no readable text", http.StatusBadRequest)
		return
	}
	format := strings.TrimPrefix(ext, ".")
	if format == "markdown" {
		format = "md"
	}
	writeJSON(w, map[string]any{
		"ok":            true,
		"name":          filepath.Base(upload.Name),
		"source_format": format,
		"text":          text,
	})
}
