package webgui

import (
	"encoding/json"
	"net/http"

	"supercli/internal/system/uilang"
)

// handleLocaleBootstrap ships only English on the startup path. Other language
// catalogs stay embedded and are fetched individually from /locales/<code>.json.
func (s *Server) handleLocaleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	english, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		http.Error(w, "English catalog unavailable", http.StatusInternalServerError)
		return
	}
	languages, err := json.Marshal(uilang.Languages())
	if err != nil {
		http.Error(w, "Language metadata unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte("var I18N = {en:"))
	_, _ = w.Write(english)
	_, _ = w.Write([]byte("};\nvar UI_LANGUAGES = "))
	_, _ = w.Write(languages)
	_, _ = w.Write([]byte(";\n"))
}
