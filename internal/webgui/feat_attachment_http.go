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
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid attachment upload: "+err.Error(), http.StatusBadRequest)
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
	paths := make([]string, 0, maxChatAttachments)
	// Stream straight into the portable staging directory. ParseMultipartForm
	// retains megabytes of file data and spills larger files into OS temp.
	for {
		source, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "read uploaded attachment: "+err.Error(), http.StatusBadRequest)
			return
		}
		if source.FileName() == "" || (source.FormName() != "files" && source.FormName() != "file") {
			if err := source.Close(); err != nil {
				http.Error(w, "read upload field: "+err.Error(), http.StatusBadRequest)
				return
			}
			continue
		}
		if len(paths) >= maxChatAttachments {
			http.Error(w, fmt.Sprintf("too many files (maximum %d)", maxChatAttachments), http.StatusBadRequest)
			return
		}
		name := safeAttachmentName(source.FileName())
		if name == "attachment" {
			name = fmt.Sprintf("clipboard-%d", len(paths)+1)
		}
		target := uniqueAttachmentTarget(root, name)
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, "create staged attachment: "+err.Error(), http.StatusInternalServerError)
			return
		}
		limit := min(int64(maxChatAttachmentBytes), int64(maxChatAttachmentsBytes)-total) + 1
		written, copyErr := io.Copy(output, io.LimitReader(source, limit))
		closeErr := output.Close()
		if written > maxChatAttachmentBytes {
			copyErr = fmt.Errorf("file exceeds the %d-byte limit", maxChatAttachmentBytes)
		}
		if copyErr != nil || closeErr != nil {
			http.Error(w, "stage attachment: "+fmt.Sprint(errorsJoinNonNil(copyErr, closeErr)), http.StatusBadRequest)
			return
		}
		total += written
		if total > maxChatAttachmentsBytes {
			http.Error(w, fmt.Sprintf("attachments exceed the %d-byte total limit", maxChatAttachmentsBytes), http.StatusBadRequest)
			return
		}
		if err := source.Close(); err != nil {
			http.Error(w, "read uploaded attachment: "+err.Error(), http.StatusBadRequest)
			return
		}
		paths = append(paths, target)
	}
	if len(paths) == 0 {
		http.Error(w, "no files uploaded", http.StatusBadRequest)
		return
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
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if r.Method == http.MethodDelete {
		if !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "profile") {
			http.Error(w, "profile scope is required for source deletion", http.StatusBadRequest)
			return
		}
		if raw == "" {
			http.Error(w, "missing path", http.StatusBadRequest)
			return
		}
		root := filepath.Join(s.eng.DataDir(), "module-sources")
		full, err := sandbox.ResolveWithin(root, raw)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		info, err := os.Stat(full)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, map[string]any{"ok": true, "removed": false})
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !info.Mode().IsRegular() {
			http.Error(w, "source is not a regular file", http.StatusBadRequest)
			return
		}
		if err := os.Remove(full); err != nil {
			http.Error(w, "remove source: "+err.Error(), http.StatusInternalServerError)
			return
		}
		parent := filepath.Dir(full)
		if filepath.Clean(parent) != filepath.Clean(root) {
			_ = os.Remove(parent) // succeeds only when the per-upload directory is empty
		}
		writeJSON(w, map[string]any{"ok": true, "removed": true})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if raw == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	var full string
	var err error
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "profile") {
		full, err = sandbox.ResolveWithin(filepath.Join(s.eng.DataDir(), "module-sources"), raw)
	} else if strings.HasPrefix(raw, "session:") {
		full, err = s.eng.resolveSessionImagePreview(raw)
	} else if strings.HasPrefix(raw, "snapshot:") && s.eng.DataDir() != "" {
		full, err = sandbox.ResolveWithin(filepath.Join(s.eng.DataDir(), ".supercli", "snapshots"), strings.TrimPrefix(raw, "snapshot:"))
	} else {
		full, err = sandbox.ResolveSafe(s.eng.Home(), raw)
		if err != nil && filepath.IsAbs(raw) && s.eng.DataDir() != "" {
			full, err = sandbox.ResolveWithin(filepath.Join(s.eng.DataDir(), ".supercli", "snapshots"), raw)
		}
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
	profileScope := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "profile")
	if !previewableAttachmentMIME(mediaType) {
		if profileScope {
			if sourceType := profileDocumentSourceMIME(info.Name()); sourceType != "" {
				mediaType = sourceType
			} else {
				http.Error(w, "profile source type is not available", http.StatusUnsupportedMediaType)
				return
			}
		} else {
			http.Error(w, "preview is available only for supported images, PDF, video and audio files", http.StatusUnsupportedMediaType)
			return
		}
	}
	if thumbnail := r.URL.Query().Get("thumbnail"); thumbnail != "" {
		s.serveAttachmentThumbnail(w, r, file, info, full, mediaType, thumbnail)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func profileDocumentSourceMIME(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return ""
	}
}

func previewableAttachmentMIME(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0])) {
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp",
		"video/mp4", "video/webm", "audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav", "audio/ogg", "application/ogg":
		return true
	default:
		return false
	}
}
