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
	for _, required := range []string{"SuperCliUI", "readSSE", "createComposerDraftStore", "normalizeFileChanges", "mutationKind", "terminalSeen"} {
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

func TestModelPaletteExcludesHiddenModels(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"function paletteModels(models)",
		"filter(function (m) { return !m.hidden; })",
		"paletteModels(modelCache).forEach",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("model palette no longer excludes hidden models: missing %q", required)
		}
	}
}

func TestQueuedMessagesCanBeEditedAndReordered(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"async function editQueuedTask(item)",
		"async function moveQueuedTask(item, position)",
		"async function chooseQueuedTaskPosition(item)",
		"function wireQueuedTaskDrag(row, handle, item)",
		"function queuePositionButton(item, index)",
		"function reorderQueuedTaskDOM(sourceRow, targetRow, clientY)",
		"function finishQueuedTaskDrag(commit)",
		"host.insertBefore(sourceRow, targetRow.nextSibling)",
		"event.dataTransfer.setDragImage(row",
		`JSON.stringify({ id: item.id, prompt: prompt })`,
		`JSON.stringify({ id: item.id, position: position })`,
		`text.addEventListener("click", function () { editQueuedTask(item); })`,
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("queued-message editing/reordering is missing %q", required)
		}
	}
	content, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(content)
	for _, required := range []string{".queue-drag", ".queue-drop-before", ".queue-drop-after"} {
		if !strings.Contains(css, required) {
			t.Fatalf("queue drag styling is missing %q", required)
		}
	}
}

func TestQueuedMessageOpensItsConversationBeforeSending(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"return await resumeSession(item.session_id, sessionByID[item.session_id] || null)",
		"if (!await prepareQueuedTask(item))",
		"if (!immediate.id || await removeQueuedTask(immediate.id)) next = immediate",
		"if (!await prepareQueuedTask(queued))",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("queued message can run outside its visible conversation: missing %q", required)
		}
	}
}

func TestStoppedRunRecoversMessageRewind(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"async function addLatestMessageRewind(node, text, attempts)",
		"/api/sessions?limit=6",
		"stopped ? 8 : 1",
		"activeSessionID = sessionID",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("stopped-run rewind recovery is missing %q", required)
		}
	}
}

func TestHardProtocolLongTranscriptRenderingIsBounded(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"var transcriptPageSize = 60",
		"var transcriptFollowTail = true",
		"smartScrollFrame = requestAnimationFrame",
		"if ((!transcriptFollowTail && !smartScrollForced) || smartScrollFrame !== null) return",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("long-transcript rendering guard is missing %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"content-visibility: auto", "contain-intrinsic-size: auto 72px"} {
		if !strings.Contains(string(css), required) {
			t.Fatalf("offscreen transcript containment is missing %q", required)
		}
	}
}

func TestLiveAssistantSegmentSurvivesFollowingToolCalls(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"var transcriptLiveAppend = false",
		"function sealAssistantSegment(node)",
		"sealAssistantSegment(current);",
		"if (!current || current._sealed) current = addAssistantMsg()",
		"function releaseLiveTranscriptBlocks()",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("live assistant/tool boundary is missing %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".stream > .transcript-live { content-visibility: visible; }") {
		t.Fatal("live transcript blocks can still be culled by content-visibility")
	}
}

func TestBulkFileChangesStayGroupedAndCollapsed(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"row.open = changes.length <= 8",
		"function commonFileChangeDirectory(changes)",
		"file-change-group-list",
		"t(\"change.\" + kind) + \" · \" + grouped[kind].length",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("bulk file-change summary is missing %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{".file-change-counts", ".file-change-group-header", "max-height: 320px; overflow: auto"} {
		if !strings.Contains(string(css), required) {
			t.Fatalf("bulk file-change styles are missing %q", required)
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

func TestProviderChooserFiltersVisibleCards(t *testing.T) {
	js := readEmbeddedAppJS(t)
	for _, required := range []string{
		"function renderProviderChooser(templates)",
		"normalizeProviderSearch",
		`classList.toggle("provider-template-filtered", !show)`,
		`renderProviderForm(templates, null, tpl)`,
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("provider chooser is missing %q", required)
		}
	}
	css, err := assetsFS.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		".provider-template-filtered { display: none !important; }",
		".provider-chooser-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }",
	} {
		if !strings.Contains(string(css), required) {
			t.Fatalf("provider chooser styles are missing %q", required)
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
