package webgui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)


func loadFontManifest(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "LICENSES", "fonts", "MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m []map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Every embedded font is listed in LICENSES/fonts/MANIFEST.json with its
// source, licence file and SHA-256, has an @font-face in fonts.css, and is
// served by the GUI with a font media type.
func TestEmbeddedFontsManifestFacesAndLicences(t *testing.T) {
	manifest := loadFontManifest(t)
	if len(manifest) < 10 {
		t.Fatalf("manifest lists %d fonts", len(manifest))
	}
	css, err := assetsFS.ReadFile("assets/fonts.css")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer(nil, false).Handler())
	defer srv.Close()
	for _, f := range manifest {
		file, family := f["file"].(string), f["family"].(string)
		data, err := assetsFS.ReadFile("assets/fonts/" + file)
		if err != nil {
			t.Fatalf("%s not embedded: %v", file, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f["sha256"] {
			t.Errorf("%s sha256 differs from MANIFEST.json", file)
		}
		face := regexp.MustCompile(`@font-face \{ font-family: "` + regexp.QuoteMeta(family) + `"; src: url\("fonts/` + regexp.QuoteMeta(file) + `"\)`)
		if !face.Match(css) {
			t.Errorf("fonts.css has no @font-face for %s (%s)", family, file)
		}
		lic := filepath.Join("..", "..", "LICENSES", "fonts", f["license_file"].(string))
		text, err := os.ReadFile(lic)
		if err != nil || !strings.Contains(strings.ToUpper(string(text)), "SIL OPEN FONT LICENSE") {
			t.Errorf("%s: licence %s missing or not OFL: %v", file, lic, err)
		}
		if src, _ := f["source"].(string); src == "" {
			t.Errorf("%s has no source", file)
		}
		resp, err := http.Get(srv.URL + "/fonts/" + file)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(body) != len(data) || !strings.HasPrefix(resp.Header.Get("Content-Type"), "font/") {
			t.Errorf("GET /fonts/%s: %d, %d bytes, %q", file, resp.StatusCode, len(body), resp.Header.Get("Content-Type"))
		}
	}
	// Every family the settings offer as embedded has a face.
	js, err := assetsFS.ReadFile("assets/js/02-ui-settings.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`'"(SuperCli [^"]+)"`).FindAllSubmatch(js, -1) {
		if !strings.Contains(string(css), `font-family: "`+string(m[1])+`"`) {
			t.Errorf("settings use %q but fonts.css has no @font-face for it", m[1])
		}
	}
}
