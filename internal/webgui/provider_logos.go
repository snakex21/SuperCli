package webgui

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

// A user can pick a built-in logo for any provider or upload an image. The
// choice lives in <data>/provider-logos/index.json keyed by provider name
// (kept on rename, dropped on delete); uploaded files sit next to it, named
// by content hash. config.toml is never touched for this.

const maxProviderLogoBytes = 512 << 10

type providerLogo struct {
	Builtin string `json:"builtin,omitempty"` // a built-in icon key
	File    string `json:"file,omitempty"`    // uploaded file name in provider-logos/
}

var providerLogoMu sync.Mutex

func providerLogoDir(dataDir string) string { return filepath.Join(dataDir, "provider-logos") }

func loadProviderLogos(dataDir string) map[string]providerLogo {
	out := map[string]providerLogo{}
	data, err := os.ReadFile(filepath.Join(providerLogoDir(dataDir), "index.json"))
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func saveProviderLogos(dataDir string, logos map[string]providerLogo) error {
	dir := providerLogoDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(logos, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "index.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "index.json"))
}

// renameProviderLogo moves a logo choice to the provider's new name.
func renameProviderLogo(dataDir, oldName, newName string) error {
	providerLogoMu.Lock()
	defer providerLogoMu.Unlock()
	logos := loadProviderLogos(dataDir)
	logo, ok := logos[oldName]
	if !ok || oldName == newName {
		return nil
	}
	delete(logos, oldName)
	logos[newName] = logo
	return saveProviderLogos(dataDir, logos)
}

// removeProviderLogo forgets a deleted provider's logo (and its file when no
// other provider uses the same image).
func removeProviderLogo(dataDir, name string) error {
	providerLogoMu.Lock()
	defer providerLogoMu.Unlock()
	logos := loadProviderLogos(dataDir)
	logo, ok := logos[name]
	if !ok {
		return nil
	}
	delete(logos, name)
	if err := saveProviderLogos(dataDir, logos); err != nil {
		return err
	}
	removeUnusedLogoFile(dataDir, logos, logo.File)
	return nil
}

func removeUnusedLogoFile(dataDir string, logos map[string]providerLogo, file string) {
	if file == "" {
		return
	}
	for _, other := range logos {
		if other.File == file {
			return
		}
	}
	_ = os.Remove(filepath.Join(providerLogoDir(dataDir), file))
}

// providerLogoImage checks an uploaded image and returns the bytes to store
// and the file extension. SVG is sanitized; raster formats are checked by
// their content, not by the file name.
func providerLogoImage(data []byte) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", errors.New("empty file")
	}
	if len(data) > maxProviderLogoBytes {
		return nil, "", errProviderLogoTooBig
	}
	switch http.DetectContentType(data) {
	case "image/png":
		return data, ".png", nil
	case "image/jpeg":
		return data, ".jpg", nil
	case "image/webp":
		return data, ".webp", nil
	}
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if bytes.HasPrefix(trimmed, []byte("<")) {
		clean, err := sanitizeSVG(trimmed)
		if err != nil {
			return nil, "", err
		}
		return clean, ".svg", nil
	}
	return nil, "", errProviderLogoType
}

var (
	errProviderLogoTooBig = errors.New("logo is larger than 512 KB")
	errProviderLogoType   = errors.New("logo is not a PNG, JPEG, WebP or SVG image")
)

// svgDroppedElements are removed with everything inside them.
var svgDroppedElements = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true, "object": true, "embed": true,
	"audio": true, "video": true, "animate": true, "set": true, "animatemotion": true,
	"animatetransform": true, "handler": true, "listener": true,
}

// sanitizeSVG rewrites an SVG keeping only drawing: no scripts, no event
// handlers, no foreignObject or embedded documents, no references outside
// the file (href/xlink:href only to "#id" or data:image/*, no url() to
// anything but "#id"). Processing instructions and doctypes are dropped.
func sanitizeSVG(data []byte) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = map[string]string{}
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	skip := 0
	sawSVG := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid SVG: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if skip > 0 || svgDroppedElements[name] {
				skip++
				continue
			}
			if !sawSVG {
				if name != "svg" {
					return nil, errProviderLogoType
				}
				sawSVG = true
			}
			t.Attr = safeSVGAttrs(t.Attr)
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}
		case xml.CharData:
			if skip == 0 && sawSVG {
				if err := enc.EncodeToken(t); err != nil {
					return nil, err
				}
			}
		}
		// Comments, processing instructions and directives are dropped.
	}
	if !sawSVG {
		return nil, errProviderLogoType
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func safeSVGAttrs(attrs []xml.Attr) []xml.Attr {
	kept := attrs[:0]
	for _, a := range attrs {
		local := strings.ToLower(a.Name.Local)
		value := strings.TrimSpace(a.Value)
		lower := strings.ToLower(value)
		switch {
		case strings.HasPrefix(local, "on"):
			continue
		case local == "href" || local == "src":
			if !strings.HasPrefix(value, "#") && !strings.HasPrefix(lower, "data:image/png") && !strings.HasPrefix(lower, "data:image/jpeg") && !strings.HasPrefix(lower, "data:image/webp") {
				continue
			}
		case local == "style" || strings.Contains(lower, "url("):
			if strings.Contains(lower, "javascript:") || strings.Contains(lower, "expression(") || strings.Contains(lower, "@import") || hasExternalURL(lower) {
				continue
			}
		}
		if strings.Contains(lower, "javascript:") {
			continue
		}
		kept = append(kept, a)
	}
	return kept
}

// hasExternalURL reports a url(...) that points anywhere but "#id".
func hasExternalURL(value string) bool {
	for rest := value; ; {
		i := strings.Index(rest, "url(")
		if i < 0 {
			return false
		}
		target := strings.TrimLeft(rest[i+4:], " '\"")
		if !strings.HasPrefix(target, "#") {
			return true
		}
		rest = rest[i+4:]
	}
}

// handleProviderIcons lists the built-in logos for the picker.
func (s *Server) handleProviderIcons(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, builtinProviderIcons())
}

// handleProviderLogo serves an uploaded logo (GET ?name=) or changes a
// provider's logo (POST {name, builtin} | {name, filename, data} | {name, reset}).
func (s *Server) handleProviderLogo(w http.ResponseWriter, r *http.Request) {
	dataDir := s.eng.DataDir()
	switch r.Method {
	case http.MethodGet:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		providerLogoMu.Lock()
		logo := loadProviderLogos(dataDir)[name]
		providerLogoMu.Unlock()
		if logo.File == "" || logo.File != filepath.Base(logo.File) {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(providerLogoDir(dataDir), logo.File))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		contentType := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".svg": "image/svg+xml"}[filepath.Ext(logo.File)]
		if contentType == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(data)
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxProviderLogoBytes*2)
		var req struct {
			Name     string `json:"name"`
			Builtin  string `json:"builtin"`
			Data     string `json:"data"` // base64
			Filename string `json:"filename"`
			Reset    bool   `json:"reset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(req.Name)
		if !s.providerExists(name) {
			http.Error(w, "provider not found", http.StatusNotFound)
			return
		}
		providerLogoMu.Lock()
		defer providerLogoMu.Unlock()
		logos := loadProviderLogos(dataDir)
		previous := logos[name]
		switch {
		case req.Reset:
			delete(logos, name)
		case req.Builtin != "":
			if !isBuiltinProviderIcon(req.Builtin) {
				http.Error(w, "unknown logo", http.StatusBadRequest)
				return
			}
			logos[name] = providerLogo{Builtin: req.Builtin}
		case req.Data != "":
			raw, err := base64.StdEncoding.DecodeString(req.Data)
			if err != nil {
				http.Error(w, "invalid image data", http.StatusBadRequest)
				return
			}
			clean, ext, err := providerLogoImage(raw)
			if err != nil {
				code := "prov.logoInvalid"
				if errors.Is(err, errProviderLogoTooBig) {
					code = "prov.logoTooBig"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "code": code})
				return
			}
			sum := sha256.Sum256(clean)
			file := hex.EncodeToString(sum[:12]) + ext
			if err := os.MkdirAll(providerLogoDir(dataDir), 0o755); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := os.WriteFile(filepath.Join(providerLogoDir(dataDir), file), clean, 0o644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			logos[name] = providerLogo{File: file}
		default:
			http.Error(w, "nothing to change", http.StatusBadRequest)
			return
		}
		if err := saveProviderLogos(dataDir, logos); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if previous.File != "" && previous.File != logos[name].File {
			removeUnusedLogoFile(dataDir, logos, previous.File)
		}
		writeJSON(w, map[string]any{"ok": true, "logo": providerLogoView(name, logos[name])})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) providerExists(name string) bool {
	if name == "" {
		return false
	}
	for _, p := range s.eng.providerManager().Configured() {
		if p.Name == name {
			return true
		}
	}
	return false
}

// providerLogoView is the logo as the GUI needs it: a built-in key, or the
// URL of the uploaded file (versioned by its content hash).
func providerLogoView(name string, logo providerLogo) map[string]string {
	switch {
	case logo.Builtin != "":
		return map[string]string{"builtin": logo.Builtin}
	case logo.File != "":
		return map[string]string{"url": "/api/provider/logo?name=" + url.QueryEscape(name) + "&v=" + strings.TrimSuffix(logo.File, filepath.Ext(logo.File))}
	}
	return nil
}

// providerUsage tells the delete dialog whether removing the provider leaves
// the selected model or saved choices (modes, roles, settings) without it.
func (s *Server) handleProviderUsage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	active, _, _ := s.eng.RuntimeSelection()
	refs := false
	if name != "" {
		if model, provider := LastModel(s.eng.DataDir()); provider == name && model != "" {
			refs = true
		}
		global, project := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())
		for _, path := range []string{global, project} {
			if path == "" {
				continue
			}
			if cfg, err := config.LoadToml(path); err == nil && config.RenameProviderRefs(&cfg, name, name+"\x00probe") {
				refs = true
			}
		}
	}
	writeJSON(w, map[string]bool{"active": name != "" && active == name, "refs": refs})
}

// providerRow is a configured provider as the GUI lists it, plus its logo:
// Icon is the built-in logo matched from type and address, Logo the user's
// own choice (built-in key or uploaded file URL), when any.
type providerRow struct {
	providers.ProviderInfo
	Icon string            `json:"Icon,omitempty"`
	Logo map[string]string `json:"Logo,omitempty"`
}

func (s *Server) providersWithLogos(list []providers.ProviderInfo) []providerRow {
	providerLogoMu.Lock()
	logos := loadProviderLogos(s.eng.DataDir())
	providerLogoMu.Unlock()
	out := make([]providerRow, 0, len(list))
	for _, p := range list {
		out = append(out, providerRow{ProviderInfo: p, Icon: ProviderIconKey(p.Type, p.BaseURL), Logo: providerLogoView(p.Name, logos[p.Name])})
	}
	return out
}

type templateRow struct {
	providers.PredefinedProvider
	Icon string `json:"Icon,omitempty"`
}

func templatesWithIcons(list []providers.PredefinedProvider) []templateRow {
	out := make([]templateRow, 0, len(list))
	for _, t := range list {
		out = append(out, templateRow{PredefinedProvider: t, Icon: ProviderIconKey(t.Type, t.BaseURL)})
	}
	return out
}
