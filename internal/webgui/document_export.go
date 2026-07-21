package webgui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"supercli/internal/tools/office"
)

const maxDocumentExportBytes = 8 << 20

var unsafeExportFilename = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

type documentExportRequest struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Text     string `json:"text"`
}

func (s *Server) handleDocumentExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentExportBytes)
	var req documentExportRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	if req.Text == "" {
		http.Error(w, "document text is empty", http.StatusBadRequest)
		return
	}
	if req.Format == "markdown" {
		req.Format = "md"
	}
	if req.Format != "docx" && req.Format != "md" && req.Format != "txt" {
		http.Error(w, "unsupported document format", http.StatusBadRequest)
		return
	}

	name := safeExportFilename(req.Filename, req.Format)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	switch req.Format {
	case "docx":
		var out bytes.Buffer
		if err := office.WriteSimpleDocx(&out, req.Text); err != nil {
			http.Error(w, "create Word document: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Header().Set("Content-Length", fmt.Sprint(out.Len()))
		_, _ = w.Write(out.Bytes())
	case "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(req.Text))
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(req.Text))
	}
}

func safeExportFilename(name, format string) string {
	base := strings.TrimSpace(filepath.Base(name))
	if base == "" || base == "." {
		base = "transkrypcja"
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSpace(unsafeExportFilename.ReplaceAllString(base, "-"))
	base = strings.Trim(base, ". ")
	if base == "" {
		base = "transkrypcja"
	}
	if len([]rune(base)) > 120 {
		base = string([]rune(base)[:120])
	}
	return base + "." + format
}
