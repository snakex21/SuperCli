package webgui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleFilePicker opens the operating system's file selector. It returns
// paths only; chat submission validates them again through the live sandbox.
func (s *Server) handleFilePicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	paths, err := pickDesktopFiles(s.filePickerHome(), s.uiLanguage())
	if err != nil {
		http.Error(w, "dialog failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	paths, err = stagePickedAttachments(s.eng.Home(), paths)
	if err != nil {
		http.Error(w, "could not prepare attachments: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"paths": paths, "workspace": s.eng.Home()})
}

func (s *Server) filePickerHome() string {
	if !strings.EqualFold(s.runtimeAppName(), "NestCafe") {
		return s.eng.Home()
	}
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return s.eng.Home()
	}
	for _, name := range []string{"Documents", "Desktop"} {
		candidate := filepath.Join(userHome, name)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
	}
	return userHome
}
