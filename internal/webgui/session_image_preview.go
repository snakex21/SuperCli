package webgui

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"supercli/internal/tools/sandbox"
)

var sessionImageFilename = regexp.MustCompile(`^[a-f0-9]{64}\.(png|jpg|jpeg|gif|webp)$`)

// Only content-addressed session files become portable preview handles. No
// absolute directory, inline base64, remote URL, or provider credential escapes.
func sessionImagePreviewPath(sessionID, path string) string {
	name := filepath.Base(strings.ReplaceAll(path, "\\", "/"))
	if sessionID == "" || strings.ContainsAny(sessionID, "/\\:") || !sessionImageFilename.MatchString(name) {
		return ""
	}
	return "session:" + sessionID + "/" + name
}

func (e *Engine) resolveSessionImagePreview(token string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(token, "session:"), "/")
	if len(parts) != 2 || parts[0] == "" || strings.ContainsAny(parts[0], "\\:") || !sessionImageFilename.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid session image reference")
	}
	store, err := e.sessionStore()
	if err != nil {
		return "", err
	}
	meta, err := store.Get(parts[0])
	if err != nil {
		return "", err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return "", errSessionOutsideWorkspace
	}
	sum := sha256.Sum256([]byte(parts[0]))
	root := filepath.Join(e.DataDir(), "session-media", fmt.Sprintf("%x", sum[:16]))
	return sandbox.ResolveWithin(root, parts[1])
}
