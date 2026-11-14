package webgui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"supercli/internal/tools"
)

const (
	nestCafeUpdateManifestURL = "https://github.com/snakex21/NestCafe/releases/latest/download/native-update.json"
	thunderbirdBridgeRepoURL  = "https://github.com/snakex21/Thunderbird-AI-Bridge"
)

type nestCafeUpdateManifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

func (s *Server) handleNestCafeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	current := strings.TrimSpace(r.URL.Query().Get("current"))
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, nestCafeUpdateManifestURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "NestCafe-update-check")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "update check: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "update check: "+resp.Status, http.StatusBadGateway)
		return
	}
	var manifest nestCafeUpdateManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&manifest); err != nil {
		http.Error(w, "update manifest: "+err.Error(), http.StatusBadGateway)
		return
	}
	manifest.Version = strings.TrimSpace(manifest.Version)
	writeJSON(w, map[string]any{
		"ok":               true,
		"current":          current,
		"latest":           manifest.Version,
		"update_available": current != "" && newerNestCafeVersion(manifest.Version, current),
		"installer_url":    manifest.URL,
		"sha256":           manifest.SHA256,
	})
}

func newerNestCafeVersion(candidate, current string) bool {
	parse := func(value string) []int {
		value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
		parts := strings.SplitN(value, "-", 2)
		segments := strings.Split(parts[0], ".")
		out := make([]int, 3)
		for i := range out {
			if i < len(segments) {
				out[i], _ = strconv.Atoi(segments[i])
			}
		}
		return out
	}
	a := parse(candidate)
	b := parse(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (s *Server) bundledThunderbirdXPI() string {
	candidates := make([]string, 0, 3)
	if dataDir := strings.TrimSpace(s.eng.DataDir()); dataDir != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(dataDir), "integrations", "Thunderbird-AI-Bridge.xpi"))
	}
	if executable, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(exeDir, "integrations", "Thunderbird-AI-Bridge.xpi"),
			filepath.Join(filepath.Dir(exeDir), "integrations", "Thunderbird-AI-Bridge.xpi"),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			if absolute, absErr := filepath.Abs(candidate); absErr == nil {
				return absolute
			}
		}
	}
	return ""
}

func (s *Server) handleThunderbirdXPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := s.bundledThunderbirdXPI()
	if path == "" {
		http.Error(w, "bundled Thunderbird AI Bridge is not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Type", "application/x-xpinstall")
	w.Header().Set("Content-Disposition", `attachment; filename="Thunderbird-AI-Bridge.xpi"`)
	http.ServeFile(w, r, path)
}

func (s *Server) handleThunderbirdIntegration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bundled := s.bundledThunderbirdXPI() != ""
	bridge := tools.ThunderbirdBridgeStatusFor(s.eng.DataDir())
	payload := map[string]any{
		"ok":                true,
		"connected":         bridge.Connected,
		"paired":            bridge.Paired,
		"bridge":            bridge,
		"repo_url":          thunderbirdBridgeRepoURL,
		"bundled_available": bundled,
		"host":              bridge.Addr,
	}
	if !bridge.Connected {
		// Asking a disconnected bridge would only wait for its timeout.
		payload["error"] = bridge.Message
		writeJSON(w, payload)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	tool := tools.NewThunderbirdMail(s.eng.DataDir()).Spec()
	result, callErr := tool.Fn(ctx, json.RawMessage(`{"op":"status"}`))
	switch {
	case callErr != nil:
		payload["connected"], payload["error"] = false, callErr.Error()
	case result.Err != nil:
		payload["connected"], payload["error"] = false, result.Err.Error()
	default:
		var status any
		if text := strings.TrimSpace(result.Text); text != "" {
			if err := json.Unmarshal([]byte(text), &status); err != nil {
				status = text
			}
		}
		payload["status"] = status
	}
	writeJSON(w, payload)
}

// handleThunderbirdPair opens a five-minute pairing window and returns the
// one-time code. The code is shown in the UI only; it is never logged.
func (s *Server) handleThunderbirdPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code, err := tools.StartThunderbirdPairing(s.eng.DataDir())
	if err != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "code": code.Code, "expires_at": code.ExpiresAt, "host_id": code.HostID})
}

func (s *Server) handleThunderbirdPairCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := tools.CancelThunderbirdPairing(s.eng.DataDir()); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleThunderbirdUnpair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := tools.UnpairThunderbird(s.eng.DataDir()); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
