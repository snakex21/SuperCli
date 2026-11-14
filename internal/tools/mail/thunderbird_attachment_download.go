package mail

import (
	"crypto/sha256"
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
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	// Session, clock and nonce are checked before the upload is accepted; the
	// MAC over the body is checked after it was streamed to a temporary file,
	// which is deleted unless the signature is valid.
	sess, nonce, reason := b.precheckSigned(r)
	if reason != "" {
		thunderbirdAuthError(w, http.StatusUnauthorized, reason, "authenticate again")
		return
	}
	signed := &thunderbirdSigned{session: sess, nonce: nonce}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" || len(id) > 160 {
		thunderbirdAuthError(w, http.StatusBadRequest, "bad_request", "missing or invalid transfer id")
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

	dir, err := b.attachmentCacheDir()
	if err != nil {
		thunderbirdAuthError(w, http.StatusInternalServerError, "internal", "cannot create attachment cache")
		return
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	file, err := os.CreateTemp(dir, "attachment-*"+ext)
	if err != nil {
		thunderbirdAuthError(w, http.StatusInternalServerError, "internal", "cannot create attachment file")
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

	hash := sha256.New()
	limited := http.MaxBytesReader(w, r.Body, maxThunderbirdAttachmentBytes)
	written, err := io.Copy(io.MultiWriter(file, hash), limited)
	if err != nil {
		thunderbirdAuthError(w, http.StatusRequestEntityTooLarge, "too_large", "attachment exceeds limit or could not be written")
		return
	}
	if reason := b.finishSigned(r, sess, nonce, b64.EncodeToString(hash.Sum(nil))); reason != "" {
		thunderbirdAuthError(w, http.StatusUnauthorized, reason, "authenticate again")
		return
	}
	if err := file.Close(); err != nil {
		signed.writeError(w, http.StatusInternalServerError, "cannot finish attachment file")
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
	if b.downloads == nil {
		b.downloads = make(map[string]thunderbirdDownloadedAttachment)
	}
	if old, ok := b.downloads[id]; ok {
		_ = os.Remove(old.Path)
	}
	b.downloads[id] = item
	b.mu.Unlock()

	signed.writeJSON(w, http.StatusOK, map[string]any{
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
		if image = readThunderbirdVision(item.Path); image != nil {
			payload["contentType"] = image.MediaType
			payload["visionAttached"] = true
		}
	}
	formatted, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: cannot format attachment result: %w", err)}, nil
	}
	return Result{Text: string(formatted), Image: image}, nil
}
