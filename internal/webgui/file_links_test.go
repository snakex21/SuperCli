package webgui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

func TestParseFileLinkTarget(t *testing.T) {
	cases := []struct {
		in   string
		path string
		line int
	}{
		{"build-migration/pkg/mczig_client.exe", filepath.FromSlash("build-migration/pkg/mczig_client.exe"), 0},
		{"src/native_bed.zig:42", filepath.FromSlash("src/native_bed.zig"), 42},
		{"src/native_bed.zig:42:7", filepath.FromSlash("src/native_bed.zig"), 42},
		{"src/a%20b.go", filepath.FromSlash("src/a b.go"), 0},
		{"src/a.go#L9", filepath.FromSlash("src/a.go"), 9},
	}
	if filepath.Separator == '\\' {
		cases = append(cases,
			struct {
				in   string
				path string
				line int
			}{`C:\Users\me\a.go`, `C:\Users\me\a.go`, 0},
			struct {
				in   string
				path string
				line int
			}{`C:/Users/me/a.go:12`, `C:\Users\me\a.go`, 12},
			struct {
				in   string
				path string
				line int
			}{`file:///C:/Users/me/a.go`, `C:\Users\me\a.go`, 0},
		)
	}
	for _, c := range cases {
		path, line := parseFileLinkTarget(c.in)
		if path != c.path || line != c.line {
			t.Errorf("parseFileLinkTarget(%q) = %q, %d; want %q, %d", c.in, path, line, c.path, c.line)
		}
	}
}

// Relative links resolve in the session's project and may not leave it;
// a missing file is reported, not served.
func TestFileLinkResolutionAndExecutablesNeverRun(t *testing.T) {
	srv := newTestServer(t, false)
	home := srv.eng.Home()
	pkg := filepath.Join(home, "build-migration", "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(pkg, "mczig_client.exe")
	if err := os.WriteFile(exe, []byte("MZ not really"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "notes.md"), []byte("# a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := srv.resolveFileLink("build-migration/pkg/mczig_client.exe")
	if err != nil || !info.Exists || info.Kind != "executable" || !info.Executable || !info.InWorkspace || info.Path != exe {
		t.Fatalf("exe link: %+v %v", info, err)
	}
	if info, _ := srv.resolveFileLink("build-migration/pkg/notes.md:2"); info.Kind != "text" || info.Line != 2 {
		t.Fatalf("text link: %+v", info)
	}
	if info, _ := srv.resolveFileLink("build-migration/pkg"); info.Kind != "dir" {
		t.Fatalf("folder link: %+v", info)
	}
	if info, err := srv.resolveFileLink("build-migration/pkg/missing.zig"); err != nil || info.Exists {
		t.Fatalf("missing file: %+v %v", info, err)
	}
	if _, err := srv.resolveFileLink("../../Windows/System32/cmd.exe"); err == nil {
		t.Fatal("a relative link left the project")
	}

	// "open" never starts a program; "reveal" selects it in the folder.
	var revealed []string
	old := revealCommand
	revealCommand = func(path string, isDir bool) *exec.Cmd {
		revealed = append(revealed, path)
		return exec.Command(os.Args[0], "-test.run=^$")
	}
	t.Cleanup(func() { revealCommand = old })
	call := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/file-link", body))
		return rec
	}
	if rec := call(`{"target":"build-migration/pkg/mczig_client.exe","action":"open"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("open of an exe: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(`{"target":"build-migration/pkg/mczig_client.exe","action":"reveal"}`); rec.Code != http.StatusOK || len(revealed) != 1 || revealed[0] != exe {
		t.Fatalf("reveal of an exe: %d %s revealed=%v", rec.Code, rec.Body.String(), revealed)
	}
	if rec := call(`{"target":"build-migration/pkg/missing.zig"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"exists":false`) {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body.String())
	}
	// The older "open path" action reveals programs instead of starting them.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/path/open", `{"path":"build-migration/pkg/mczig_client.exe"}`))
	if rec.Code != http.StatusOK || len(revealed) != 2 {
		t.Fatalf("open path on an exe: %d %s revealed=%v", rec.Code, rec.Body.String(), revealed)
	}
}

// The chat renderer sends web links to the preview dialog and everything
// that names a file on this computer to the file action.
func TestChatLinkClassificationNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1000, Height: 700})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, api.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	var ready bool
	for i := 0; i < 200 && !ready; i++ {
		_ = page.Eval(ctx, `typeof classifyChatLink==='function'`, &ready)
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("GUI did not start")
	}
	var got map[string]string
	script := `(()=>{const o=location.origin,out={};
for (const h of ['build-migration/survival-package/stage35/mczig_client.exe','./src/a.zig:42','C:\\Users\\me\\a.go','C:/x/y.txt','file:///C:/x/y.txt',
  'https://ziglang.org/doc', o+'/build-migration/x.exe', o+'/api/files', 'mailto:a@b.c', '#top']) {
  const c=classifyChatLink(h,o); out[h]=c.kind+':'+(c.target||c.url||'');
}
return out;})()`
	if err := page.Eval(ctx, script, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"build-migration/survival-package/stage35/mczig_client.exe": "file:build-migration/survival-package/stage35/mczig_client.exe",
		"./src/a.zig:42":                   "file:src/a.zig:42",
		`C:\Users\me\a.go`:                 `file:C:\Users\me\a.go`,
		"C:/x/y.txt":                       "file:C:/x/y.txt",
		"file:///C:/x/y.txt":               "file:file:///C:/x/y.txt",
		"https://ziglang.org/doc":          "web:https://ziglang.org/doc",
		api.URL + "/build-migration/x.exe": "file:build-migration/x.exe",
		api.URL + "/api/files":             "other:",
		"mailto:a@b.c":                     "other:",
		"#top":                             "other:",
	}
	for h, w := range want {
		if got[h] != w {
			t.Errorf("classifyChatLink(%q) = %q, want %q", h, got[h], w)
		}
	}
}
