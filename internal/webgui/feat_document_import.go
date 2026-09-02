package webgui

import (
	"fmt"
	"io"
	"mime/multipart"
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
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		http.Error(w, "invalid document upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	header := firstUploadedDocument(r)
	if header == nil {
		http.Error(w, "no document uploaded", http.StatusBadRequest)
		return
	}
	if header.Size <= 0 || header.Size > maxLocalDocumentImportBytes {
		http.Error(w, "document is empty or too large", http.StatusBadRequest)
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".docx" && ext != ".md" && ext != ".markdown" && ext != ".txt" {
		http.Error(w, "supported document formats: DOCX, MD, TXT", http.StatusUnsupportedMediaType)
		return
	}

	file, err := header.Open()
	if err != nil {
		http.Error(w, "open document: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	var text string
	switch ext {
	case ".docx":
		tmp, err := os.CreateTemp("", "nestcafe-import-*.docx")
		if err != nil {
			http.Error(w, "prepare docx import: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		written, copyErr := io.Copy(tmp, io.LimitReader(file, maxLocalDocumentImportBytes+1))
		closeErr := tmp.Close()
		if copyErr != nil || closeErr != nil || written > maxLocalDocumentImportBytes {
			http.Error(w, "read docx: "+fmt.Sprint(errorsJoinNonNil(copyErr, closeErr)), http.StatusBadRequest)
			return
		}
		text, err = office.ReadSimpleDocxMarkdown(tmpPath, maxLocalDocumentImportBytes)
		if err != nil {
			http.Error(w, "read docx: "+err.Error(), http.StatusBadRequest)
			return
		}
	default:
		data, err := io.ReadAll(io.LimitReader(file, maxLocalDocumentImportBytes+1))
		if err != nil || len(data) > maxLocalDocumentImportBytes {
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
		"name":          filepath.Base(header.Filename),
		"source_format": format,
		"text":          text,
	})
}

func firstUploadedDocument(r *http.Request) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	for _, key := range []string{"file", "document", "files"} {
		if headers := r.MultipartForm.File[key]; len(headers) > 0 {
			return headers[0]
		}
	}
	return nil
}
