package uitest

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func putSource(t *testing.T, root, name, content string) {
	t.Helper()
	name = filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSourceSnapshotAllowlistAndImmutability(t *testing.T) {
	root := t.TempDir()
	allowed := map[string]string{
		"internal/webgui/assets/new-untracked.js": "export const value = 42;",
		"internal/webgui/assets/style.css":        "body {}",
		"internal/webgui/assets/index.html":       "<!doctype html>",
		"internal/webgui/assets/locales/en.json":  `{"hello":"world"}`,
		"internal/webgui/server.go":               "package webgui\n",
		"internal/system/uilang/catalogs/en.json": `{"hello":"world"}`,
		"scripts/native.js":                       "export const native = true;",
		"test/ui/native.test.js":                  "export const native = true;",
	}
	for name, value := range allowed {
		putSource(t, root, name, value)
	}
	for _, name := range []string{"go.mod", "README.md", ".supercli/auth.json", "supercli-data/config.json", "internal/config/config.go", "scripts/.env", "scripts/auth.json", "scripts/auth-other.json", "scripts/settings.json", "scripts/credentials.json", "scripts/config.json", "scripts/data/private.json", "scripts/node_modules/private/index.js", "test/ui/.hidden/private.js", "test/ui/image.png", "test/ui/old.test.cjs"} {
		putSource(t, root, name, "DO NOT SERVE")
	}
	sources, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sources, allowed) {
		t.Fatalf("snapshot allowlist mismatch: got=%v want=%v", sources, allowed)
	}
	server, err := serveSnapshot(sources)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if !strings.HasPrefix(server.URL, "http://127.0.0.1:") {
		t.Fatalf("not bound to IPv4 loopback: %s", server.URL)
	}
	sources["scripts/native.js"] = "changed map"
	putSource(t, root, "internal/webgui/assets/new-untracked.js", "changed disk")
	client := server.Client()
	for _, item := range []struct {
		method, target, contains, contentType string
		status                                int
	}{
		{"GET", "/", "<body></body>", "text/html", 200},
		{"GET", "/__test/sources.json", "new-untracked.js", "application/json", 200},
		{"GET", "/internal/webgui/assets/new-untracked.js", "value = 42", "text/javascript", 200},
		{"GET", "/scripts/native.js?cache=none", "native = true", "text/javascript", 200},
		{"HEAD", "/test/ui/native.test.js", "", "text/javascript", 200},
		{"GET", "/go.mod", "", "", 404},
		{"GET", "/.supercli/auth.json", "", "", 404},
		{"GET", "/scripts/auth.json", "", "", 404},
		{"GET", "/scripts/../go.mod", "", "", 404},
		{"GET", "/scripts/%2e%2e/go.mod", "", "", 404},
		{"GET", "/scripts//native.js", "", "", 404},
		{"GET", "/test/ui/", "", "", 404},
		{"GET", "/api/config", "", "", 404},
		{"POST", "/__test/sources.json", "", "", 405},
	} {
		req, err := http.NewRequest(item.method, server.URL+item.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != item.status || !strings.Contains(string(body), item.contains) || !strings.Contains(response.Header.Get("Content-Type"), item.contentType) {
			t.Errorf("%s %s: status=%d type=%s body=%s", item.method, item.target, response.StatusCode, response.Header.Get("Content-Type"), body)
		}
		if item.method == "HEAD" && len(body) != 0 {
			t.Error("HEAD returned a body")
		}
		if item.target == "/__test/sources.json" && item.method == "GET" {
			var got map[string]string
			if err := json.Unmarshal(body, &got); err != nil || !reflect.DeepEqual(got, allowed) {
				t.Errorf("snapshot changed: %v %v", got, err)
			}
		}
	}
}

func TestSnapshotRejectsNonTextAndOversize(t *testing.T) {
	for _, content := range []string{"bad\x00source", string([]byte{0xff, 0xfe}), strings.Repeat("x", maxSourceSize+1)} {
		root := t.TempDir()
		putSource(t, root, "test/ui/bad.js", content)
		if _, err := snapshot(root); err == nil {
			t.Fatal("accepted binary or oversized source")
		}
	}
}

func TestSnapshotDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	putSource(t, outside, "private.js", "private")
	putSource(t, root, "test/ui/safe.js", "safe")
	link := filepath.Join(root, "test", "ui", "linked.js")
	if err := os.Symlink(filepath.Join(outside, "private.js"), link); err != nil {
		t.Logf("OS does not permit test symlinks: %v", err)
		return
	}
	if err := os.Symlink(outside, filepath.Join(root, "test", "ui", "linked-dir")); err != nil {
		t.Fatal(err)
	}
	sources, err := snapshot(root)
	if err != nil || len(sources) != 1 || sources["test/ui/safe.js"] != "safe" {
		t.Fatalf("followed source symlink: %v %v", sources, err)
	}
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "internal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(other, "internal", "webgui")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot(other); err == nil {
		t.Fatal("accepted symlinked source tree")
	}
}

func TestFileDiscoverySortedFilterAndEmpty(t *testing.T) {
	sources := map[string]string{
		"test/ui/z.test.js": "", "test/ui/a.test.js": "", "test/ui/harness.test.js": "",
		"test/ui/sub/not-direct.test.js": "", "test/ui/old.test.cjs": "", "scripts/not-ui.test.js": "",
	}
	got, err := testFiles(sources, "")
	want := []string{"test/ui/a.test.js", "test/ui/harness.test.js", "test/ui/z.test.js"}
	if err != nil || !reflect.DeepEqual(got, want) || !sort.StringsAreSorted(got) {
		t.Fatalf("test discovery: %v %v", got, err)
	}
	if got, err := testFiles(sources, "harness"); err != nil || !reflect.DeepEqual(got, want[1:2]) {
		t.Fatalf("substring filter: %v %v", got, err)
	}
	if _, err := testFiles(sources, "no-match"); err == nil {
		t.Fatal("silently accepted an empty suite")
	}
}
