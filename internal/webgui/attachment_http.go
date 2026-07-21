package webgui

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/sandbox"
)

const attachmentUploadOverhead = 1 << 20

func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatAttachmentsBytes+attachmentUploadOverhead)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "invalid attachment upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		headers = r.MultipartForm.File["file"]
	}
	if len(headers) == 0 {
		http.Error(w, "no files uploaded", http.StatusBadRequest)
		return
	}
	if len(headers) > maxChatAttachments {
		http.Error(w, fmt.Sprintf("too many files: %d (maximum %d)", len(headers), maxChatAttachments), http.StatusBadRequest)
		return
	}

	rootBase := filepath.Join(s.eng.Home(), ".supercli", "attachments")
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "profile") {
		rootBase = filepath.Join(s.eng.DataDir(), "module-sources")
	}
	root := filepath.Join(rootBase, randomDataID())
	if err := os.MkdirAll(root, 0o700); err != nil {
		http.Error(w, "create attachment directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(root)
		}
	}()

	var total int64
	paths := make([]string, 0, len(headers))
	for index, header := range headers {
		if header.Size > maxChatAttachmentBytes {
			http.Error(w, fmt.Sprintf("%q is too large", header.Filename), http.StatusBadRequest)
			return
		}
		source, err := header.Open()
		if err != nil {
			http.Error(w, "open uploaded attachment: "+err.Error(), http.StatusBadRequest)
			return
		}
		name := safeAttachmentName(header.Filename)
		if name == "attachment" {
			name = fmt.Sprintf("clipboard-%d", index+1)
		}
		target := uniqueAttachmentTarget(root, name)
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = source.Close()
			http.Error(w, "create staged attachment: "+err.Error(), http.StatusInternalServerError)
			return
		}
		written, copyErr := io.Copy(output, io.LimitReader(source, maxChatAttachmentBytes+1))
		closeErr := output.Close()
		sourceErr := source.Close()
		if written > maxChatAttachmentBytes {
			copyErr = fmt.Errorf("file exceeds the %d-byte limit", maxChatAttachmentBytes)
		}
		if copyErr != nil || closeErr != nil || sourceErr != nil {
			http.Error(w, "stage attachment: "+fmt.Sprint(errorsJoinNonNil(copyErr, closeErr, sourceErr)), http.StatusBadRequest)
			return
		}
		total += written
		if total > maxChatAttachmentsBytes {
			http.Error(w, fmt.Sprintf("attachments exceed the %d-byte total limit", maxChatAttachmentsBytes), http.StatusBadRequest)
			return
		}
		paths = append(paths, target)
	}
	keep = true
	writeJSON(w, map[string]any{"paths": paths, "workspace": s.eng.Home()})
}

func uniqueAttachmentTarget(root, name string) string {
	target := filepath.Join(root, name)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return target
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for index := 2; ; index++ {
		target = filepath.Join(root, fmt.Sprintf("%s-%d%s", base, index, ext))
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return target
		}
	}
}

func errorsJoinNonNil(errs ...error) error {
	var messages []string
	for _, err := range errs {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}

func (s *Server) handleAttachmentPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	var full string
	var err error
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "profile") {
		full, err = sandbox.ResolveWithin(filepath.Join(s.eng.DataDir(), "module-sources"), raw)
	} else {
		full, err = sandbox.ResolveSafe(s.eng.Home(), raw)
	}
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	file, err := os.Open(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxChatAttachmentBytes {
		http.Error(w, "attachment is unavailable for preview", http.StatusBadRequest)
		return
	}
	header := make([]byte, 512)
	n, readErr := file.Read(header)
	if readErr != nil && readErr != io.EOF {
		http.Error(w, readErr.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mediaType := http.DetectContentType(header[:n])
	if !previewableAttachmentMIME(mediaType) {
		http.Error(w, "preview is available only for images and PDF files", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func previewableAttachmentMIME(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0])) {
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
