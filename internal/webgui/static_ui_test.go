package webgui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// readEmbeddedAppJS loads the split WebGUI front-end modules under
// assets/js/ in lexical order (00-helpers … 12-keyboard-init) and
// joins them so string-presence tests stay independent of the file
// layout.
func readEmbeddedAppJS(t *testing.T) string {
	t.Helper()
	var names []string
	err := fs.WalkDir(assetsFS, "assets/js", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		names = append(names, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk assets/js: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no assets/js/*.js modules embedded")
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		raw, err := assetsFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestServerCustomUIRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>SuperCli bridge</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(nil, false)
	if err := srv.UseUIRoot(root); err != nil {
		t.Fatalf("UseUIRoot: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SuperCli bridge") {
		t.Fatalf("custom UI response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestServerCustomUIRootRequiresIndex(t *testing.T) {
	srv := NewServer(nil, false)
	if err := srv.UseUIRoot(t.TempDir()); err == nil {
		t.Fatal("UseUIRoot should reject a directory without index.html")
	}
}

func TestServerCustomUIFS(t *testing.T) {
	srv := NewServer(nil, false)
	fsys := os.DirFS(t.TempDir())
	if err := srv.UseUIFS(fsys); err == nil {
		t.Fatal("UseUIFS should reject a filesystem without index.html")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<title>Bundled app</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.UseUIFS(os.DirFS(root)); err != nil {
		t.Fatalf("UseUIFS: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Bundled app") {
		t.Fatalf("embedded custom UI response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestServerSharedUIRuntimeIsAvailableWithCustomUI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<title>Branded app</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(nil, false)
	if err := srv.UseUIFS(os.DirFS(root)); err != nil {
		t.Fatalf("UseUIFS: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/.__supercli/ui/runtime.js", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared runtime response = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, required := range []string{"SuperCliUI", "readSSE", "normalizeFileChanges", "mutationKind"} {
		if !strings.Contains(body, required) {
			t.Fatalf("shared runtime is missing %q", required)
		}
	}
}

func TestAttachmentUIHasThumbnailAndCenteredPreview(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{"attachmentPreviewSource", "attachment-thumbnail", "image-attachment", "renderSentAttachments", "sentAttachmentsFor"} {
		if !strings.Contains(js, required) {
			t.Fatalf("attachment UI is missing %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)
	for _, required := range []string{
		".attachment-thumbnail",
		".sent-attachment-gallery",
		".sent-attachment-preview",
		".attachment-preview-dialog",
		"position: fixed; inset: 0; margin: auto;",
		"width: min(1400px, calc(var(--app-viewport-width) - 28px));",
		"width: 100%; height: 100%; min-width: 0; min-height: 0; object-fit: contain;",
	} {
		if !strings.Contains(style, required) {
			t.Fatalf("attachment styles are missing %q", required)
		}
	}
}

func TestWebUIUsesOwnedDialogs(t *testing.T) {
	script := readEmbeddedAppJS(t)
	for _, banned := range []string{"window.prompt(", "window.confirm(", "window.alert("} {
		if strings.Contains(script, banned) {
			t.Fatalf("web UI still uses browser dialog %q", banned)
		}
	}
	for _, required := range []string{"showAppDialog", "appConfirm", "appPrompt"} {
		if !strings.Contains(script, required) {
			t.Fatalf("web UI is missing owned dialog helper %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{".app-dialog-overlay", ".app-dialog-input", ".app-dialog-danger"} {
		if !strings.Contains(string(css), required) {
			t.Fatalf("web UI is missing owned dialog style %q", required)
		}
	}
}

func TestModelContextControlIsDiscoverableInPalette(t *testing.T) {
	html, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"model-context-control", "model-context-input", "model-context-save"} {
		if !strings.Contains(string(html), required) {
			t.Fatalf("model palette is missing %q", required)
		}
	}
	js := readEmbeddedAppJS(t)
	for _, required := range []string{"renderActiveContextControl", "saveActiveModelContext", "activeProviderID"} {
		if !strings.Contains(js, required) {
			t.Fatalf("model context control is missing behavior %q", required)
		}
	}
}

func TestModelPaletteListCanShrinkAndScroll(t *testing.T) {
	content, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(content)
	for _, required := range []string{
		"height: min(560px, calc(var(--app-viewport-height) - 54px))",
		"min-height: 0; overflow-y: auto",
		"scrollbar-gutter: stable",
	} {
		if !strings.Contains(css, required) {
			t.Fatalf("model palette is missing responsive scroll rule %q", required)
		}
	}
}

func TestUIScaleKeepsLayoutInsideEffectiveViewport(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"function applyViewportScale()",
		"window.innerWidth / scale",
		"window.innerHeight / scale",
		`--app-viewport-width`,
		`--app-viewport-height`,
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("UI scale is missing effective viewport behavior %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		".shell { display: flex; width: 100%; height: 100%; }",
		"width: var(--app-viewport-width); height: var(--app-viewport-height);",
		":root.ui-scale-compact .sidebar",
	} {
		if !strings.Contains(string(css), required) {
			t.Fatalf("UI scale styles are missing %q", required)
		}
	}
}

// A window that has not been laid out yet reports innerWidth/innerHeight of
// 0. Pinning the root box to that measurement collapses the app to 1px, and
// because `body { overflow: hidden }` there is no page scrollbar to recover
// with: the transcript becomes 0px tall and cannot be scrolled at all.
func TestViewportScaleIgnoresUnmeasuredWindow(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"window.innerWidth > 0 && window.innerHeight > 0",
		"function scheduleViewportRemeasure()",
		"scheduleViewportRemeasure();",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("viewport scaling no longer guards an unmeasured window: missing %q", required)
		}
	}
	// The retry must use a timer: an unrendered window never runs animation
	// frames, so requestAnimationFrame would never recover the layout.
	remeasure := js[strings.Index(js, "function scheduleViewportRemeasure()"):]
	remeasure = remeasure[:strings.Index(remeasure, "function applyViewportScale()")]
	if strings.Contains(remeasure, "requestAnimationFrame") {
		t.Fatal("viewport re-measure must not depend on requestAnimationFrame; an unrendered window never fires it")
	}
	if !strings.Contains(remeasure, "setTimeout") {
		t.Fatal("viewport re-measure must retry on a timer")
	}
}
