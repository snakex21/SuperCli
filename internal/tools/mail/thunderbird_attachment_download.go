package mail

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxThunderbirdAttachmentBytes = int64(64 << 20)
	maxThunderbirdVisionBytes     = int64(10 << 20)
)

type thunderbirdDownloadedAttachment struct {
	Path        string
	Name        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}

type thunderbirdAttachmentBridgeResult struct {
	TransferID  string `json:"transferId"`
	Filename    string `json:"filename"`
	Extension   string `json:"extension"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	PartName    string `json:"partName"`
	Message     any    `json:"message,omitempty"`
}

func (b *thunderbirdBridgeState) cleanupDownloadedAttachmentsLocked() {
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, item := range b.downloads {
		if item.CreatedAt.Before(cutoff) {
			_ = os.Remove(item.Path)
			delete(b.downloads, id)
		}
	}
}

func (b *thunderbirdBridgeState) handleAttachmentFile(w http.ResponseWriter, r *http.Request) {
	setThunderbirdCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost || !b.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	defer r.Body.Close()

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" || len(id) > 160 {
		http.Error(w, "missing or invalid transfer id", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("filename"))
	if name == "" {
		name = "attachment.bin"
	}
	name = filepath.Base(name)
	contentType := strings.TrimSpace(r.URL.Query().Get("content_type"))
	if contentType == "" {
		contentType = strings.TrimSpace(r.Header.Get("Content-Type"))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	dir := filepath.Join(os.TempDir(), "supercli-thunderbird-attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		http.Error(w, "cannot create attachment cache", http.StatusInternalServerError)
		return
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	file, err := os.CreateTemp(dir, "attachment-*"+ext)
	if err != nil {
		http.Error(w, "cannot create attachment file", http.StatusInternalServerError)
		return
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()

	limited := http.MaxBytesReader(w, r.Body, maxThunderbirdAttachmentBytes)
	written, err := io.Copy(file, limited)
	if err != nil {
		http.Error(w, "attachment exceeds limit or could not be written", http.StatusRequestEntityTooLarge)
		return
	}
	if err := file.Close(); err != nil {
		http.Error(w, "cannot finish attachment file", http.StatusInternalServerError)
		return
	}
	keep = true

	item := thunderbirdDownloadedAttachment{
		Path:        path,
		Name:        name,
		ContentType: contentType,
		Size:        written,
		CreatedAt:   time.Now(),
	}
	b.mu.Lock()
	b.cleanupDownloadedAttachmentsLocked()
	if old, ok := b.downloads[id]; ok {
		_ = os.Remove(old.Path)
	}
	b.downloads[id] = item
	b.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"transferId":  id,
		"filename":    name,
		"contentType": contentType,
		"size":        written,
	})
}

func (b *thunderbirdBridgeState) downloadedAttachment(id string) (thunderbirdDownloadedAttachment, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupDownloadedAttachmentsLocked()
	item, ok := b.downloads[id]
	return item, ok
}

func thunderbirdVisionMIME(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a" {
		return "image/gif"
	}
	if string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

func (t *ThunderbirdMail) attachmentResult(data json.RawMessage) (Result, error) {
	var bridge thunderbirdAttachmentBridgeResult
	if err := json.Unmarshal(data, &bridge); err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: invalid attachment response: %w", err)}, nil
	}
	if strings.TrimSpace(bridge.TransferID) == "" {
		return Result{Err: fmt.Errorf("thunderbird_mail: attachment response has no transfer id")}, nil
	}
	item, ok := globalThunderbirdBridge.downloadedAttachment(bridge.TransferID)
	if !ok {
		return Result{Err: fmt.Errorf("thunderbird_mail: downloaded attachment is unavailable or expired")}, nil
	}

	payload := map[string]any{
		"filename":       item.Name,
		"extension":      strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name)), "."),
		"contentType":    item.ContentType,
		"size":           item.Size,
		"partName":       bridge.PartName,
		"localPath":      item.Path,
		"message":        bridge.Message,
		"visionAttached": false,
	}

	var image *ImageContent
	if item.Size > 0 && item.Size <= maxThunderbirdVisionBytes {
		if raw, err := os.ReadFile(item.Path); err == nil {
			if mediaType := thunderbirdVisionMIME(raw); mediaType != "" {
				payload["contentType"] = mediaType
				payload["visionAttached"] = true
				image = &ImageContent{MediaType: mediaType, Data: raw}
			}
		}
	}
	formatted, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: cannot format attachment result: %w", err)}, nil
	}
	return Result{Text: string(formatted), Image: image}, nil
}
