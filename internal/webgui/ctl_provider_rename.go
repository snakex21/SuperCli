package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"supercli/internal/llm/providers"
)

// handleProviderRename renames a configured provider and every reference to
// it: config layers (default_provider, "provider/model" settings, prices,
// hidden models), saved sessions, context budgets, the web GUI's own last
// model and vision-index choice, and the live runtime when the provider is
// active. The API key lives inside the provider entry and moves with it; it
// is never part of the request or the response.
func (s *Server) handleProviderRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name    string `json:"name"`
		NewName string `json:"new_name"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	oldName := strings.TrimSpace(req.Name)
	newName := strings.TrimSpace(req.NewName)
	if oldName == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if oldName == newName {
		writeJSON(w, map[string]any{"ok": true, "name": newName, "changed": false})
		return
	}
	activeProvider, activeModel, _ := s.eng.RuntimeSelection()
	m := s.eng.providerManager()
	store, _ := s.eng.sessionStore()
	var sessions providers.SessionProviderRenamer
	if store != nil {
		sessions = store
	}
	var budgets providers.ContextBudgetRenamer
	if s.eng.modelContexts != nil {
		budgets = s.eng.modelContexts
	}
	if err := providers.RenameProviderEverywhere(m, oldName, newName, sessions, budgets, s.eng.caps); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var warnings []string
	if err := renameWebGUIProviderRefs(s.eng.dataDir, oldName, newName); err != nil {
		warnings = append(warnings, err.Error())
	}
	runtimeRefreshed := false
	if activeProvider == oldName && activeModel != "" {
		if err := s.eng.SwitchModel(activeModel, newName); err != nil {
			warnings = append(warnings, fmt.Sprintf("active connection could not be refreshed: %v", err))
		} else {
			runtimeRefreshed = true
		}
	}
	resp := map[string]any{"ok": true, "name": newName, "old_name": oldName, "changed": true, "runtime_refreshed": runtimeRefreshed}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	writeJSON(w, resp)
}

// renameWebGUIProviderRefs updates the browser-owned settings that store a
// provider name: the last selected model and the folder-index vision model.
func renameWebGUIProviderRefs(dataDir, oldName, newName string) error {
	if model, provider := LastModel(dataDir); provider == oldName && model != "" {
		if err := saveLastModel(dataDir, model, newName); err != nil {
			return fmt.Errorf("web GUI model selection was not updated: %w", err)
		}
	}
	folderIndexMu.Lock()
	defer folderIndexMu.Unlock()
	cfg := loadFolderIndexConfig(dataDir)
	if cfg.VisionProvider == oldName {
		cfg.VisionProvider = newName
		if err := saveFolderIndexConfig(dataDir, cfg); err != nil {
			return fmt.Errorf("folder index vision provider was not updated: %w", err)
		}
	}
	return nil
}
