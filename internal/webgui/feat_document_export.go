package webgui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"supercli/internal/tools/office"
)

const maxDocumentExportBytes = 8 << 20

var unsafeExportFilename = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

type documentExportRequest struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Text     string `json:"text"`
	// Dir is an optional absolute output folder. Empty means fallback:
	// <dataDir>/exports/ocr (always available under NestCafe portable data).
	Dir string `json:"dir,omitempty"`
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
	payload, err := normalizeDocumentExport(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := safeExportFilename(payload.Filename, payload.Format)
	data, contentType, err := buildDocumentExportBytes(payload.Format, payload.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// handleDocumentExportSave writes the document to disk.
// dir empty → supercli-data/exports/ocr (fallback)
// dir set  → that absolute folder (user-chosen output folder)
func (s *Server) handleDocumentExportSave(w http.ResponseWriter, r *http.Request) {
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
	payload, err := normalizeDocumentExport(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := safeExportFilename(payload.Filename, payload.Format)
	data, _, err := buildDocumentExportBytes(payload.Format, payload.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	dir, usedFallback, err := s.resolveExportDir(payload.Dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "create export folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	target := uniqueExportPath(dir, name)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		http.Error(w, "write export file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"ok":            true,
		"path":          target,
		"dir":           dir,
		"used_fallback": usedFallback,
	})
}

func normalizeDocumentExport(req documentExportRequest) (documentExportRequest, error) {
	req.Text = strings.TrimSpace(req.Text)
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	req.Dir = strings.TrimSpace(req.Dir)
	if req.Text == "" {
		return req, fmt.Errorf("document text is empty")
	}
	if req.Format == "markdown" {
		req.Format = "md"
	}
	if req.Format != "docx" && req.Format != "md" && req.Format != "txt" {
		return req, fmt.Errorf("unsupported document format")
	}
	return req, nil
}

func buildDocumentExportBytes(format, text string) ([]byte, string, error) {
	switch format {
	case "docx":
		var out bytes.Buffer
		if err := office.WriteSimpleDocx(&out, text); err != nil {
			return nil, "", fmt.Errorf("create Word document: %w", err)
		}
		return out.Bytes(), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil
	case "md":
		return []byte(text), "text/markdown; charset=utf-8", nil
	default:
		return []byte(text), "text/plain; charset=utf-8", nil
	}
}

func (s *Server) resolveExportDir(custom string) (dir string, usedFallback bool, err error) {
	if custom == "" {
		return filepath.Join(s.eng.DataDir(), "exports", "ocr"), true, nil
	}
	clean := filepath.Clean(custom)
	if !filepath.IsAbs(clean) {
		return "", false, fmt.Errorf("export folder must be an absolute path")
	}
	if strings.Contains(clean, "..") {
		return "", false, fmt.Errorf("invalid export folder")
	}
	return clean, false, nil
}

func uniqueExportPath(dir, name string) string {
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err != nil {
		return target
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	stamp := time.Now().Format("20060102-150405")
	return filepath.Join(dir, base+"-"+stamp+ext)
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
