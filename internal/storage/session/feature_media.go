package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/llm"
)

const sessionMediaDirName = "session-media"

// ExternalizeImage implements agent.imageExternalizer without importing the
// agent package. Tool-generated image bytes are written once to the session
// media directory and history receives only a lightweight file-backed ref.
func (w *Writer) ExternalizeImage(ctx context.Context, mediaType string, data []byte) (llm.ImageRef, error) {
	if w == nil || w.store == nil {
		return llm.ImageRef{}, fmt.Errorf("session.Writer.ExternalizeImage: nil writer/store")
	}
	if err := ctx.Err(); err != nil {
		return llm.ImageRef{}, err
	}
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if mediaType == "" || len(data) == 0 {
		return llm.ImageRef{}, fmt.Errorf("session.Writer.ExternalizeImage: media type and data are required")
	}
	path, err := w.store.storeSessionImage(w.sessionID, mediaType, data)
	if err != nil {
		return llm.ImageRef{}, err
	}
	return llm.ImageRef{MediaType: mediaType, Path: path, ID: sessionImageID(data)}, nil
}

func sessionImageID(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("img_%x", sum[:6])
}

func (s *Store) storeSessionImage(sessionID, mediaType string, data []byte) (string, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return "", fmt.Errorf("session.Store.storeSessionImage: store root is empty")
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session.Store.storeSessionImage: session id is empty")
	}
	dir := s.sessionMediaDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("session.Store.storeSessionImage mkdir: %w", err)
	}
	sum := sha256.Sum256(data)
	name := fmt.Sprintf("%x%s", sum[:], imageExtension(mediaType))
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return filepath.Abs(path)
		}
		return "", fmt.Errorf("session.Store.storeSessionImage create: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("session.Store.storeSessionImage write: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("session.Store.storeSessionImage close: %w", err)
	}
	ok = true
	return filepath.Abs(path)
}

func (s *Store) sessionMediaDir(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(s.root, sessionMediaDirName, fmt.Sprintf("%x", sum[:16]))
}

func (s *Store) removeSessionMedia(sessionID string) error {
	if s == nil || strings.TrimSpace(s.root) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return os.RemoveAll(s.sessionMediaDir(sessionID))
}

func (s *Store) removeAllSessionMedia() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.root, sessionMediaDirName))
}

// externalizeModelImages upgrades legacy inline-base64 refs as sessions are
// resumed and repairs file paths after a portable data directory is moved.
// It changes only the in-memory provider context; the next saved projection is
// therefore small without requiring an eager database migration.
func (s *Store) externalizeModelImages(sessionID string, msgs []llm.Message) []llm.Message {
	for mi := range msgs {
		for pi := range msgs[mi].Parts {
			part := &msgs[mi].Parts[pi]
			if part.Type != llm.PartTypeImage || part.Image == nil {
				continue
			}
			img := part.Image
			if img.Path != "" {
				path := img.Path
				if _, err := os.Stat(path); err != nil {
					candidate := filepath.Join(s.sessionMediaDir(sessionID), filepath.Base(img.Path))
					if _, err := os.Stat(candidate); err != nil {
						continue
					}
					if absolute, absErr := filepath.Abs(candidate); absErr == nil {
						candidate = absolute
					}
					path = candidate
				}
				copy := *img
				copy.Path = path
				copy.Active = false
				if copy.ID == "" {
					if raw, err := os.ReadFile(path); err == nil {
						copy.ID = sessionImageID(raw)
					}
				}
				part.Image = &copy
				continue
			}
			if img.URL != "" || img.Data == "" || img.MediaType == "" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				continue
			}
			path, err := s.storeSessionImage(sessionID, img.MediaType, raw)
			if err != nil {
				continue
			}
			copy := *img
			copy.Data = ""
			copy.Path = path
			copy.ID = sessionImageID(raw)
			copy.Active = false
			part.Image = &copy
		}
	}
	return msgs
}

func imageExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".img"
	}
}
