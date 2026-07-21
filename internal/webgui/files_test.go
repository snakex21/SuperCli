package webgui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"supercli/internal/tools/sandbox"
)

func fileTestServer(home string) *Server {
	return &Server{eng: &Engine{home: home}}
}

func TestFilePanelRejectsSiblingPrefixEscape(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "project")
	sibling := filepath.Join(root, "project-secret")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(sibling, "secret.txt")
	if err := os.WriteFile(secret, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := fileTestServer(home)

	listReq := httptest.NewRequest(http.MethodGet, "/api/files?dir="+secretURLPath(sibling), nil)
	listRec := httptest.NewRecorder()
	srv.handleFiles(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("list sibling status = %d, want %d", listRec.Code, http.StatusForbidden)
	}

	readReq := httptest.NewRequest(http.MethodGet, "/api/file/read?path="+secretURLPath(secret), nil)
	readRec := httptest.NewRecorder()
	srv.handleFileRead(readRec, readReq)
	if readRec.Code != http.StatusForbidden {
		t.Fatalf("read sibling status = %d, want %d", readRec.Code, http.StatusForbidden)
	}

	body, err := json.Marshal(map[string]string{"path": secret, "content": "overwritten"})
	if err != nil {
		t.Fatal(err)
	}
	writeReq := httptest.NewRequest(http.MethodPost, "/api/file/write", bytes.NewReader(body))
	writeRec := httptest.NewRecorder()
	srv.handleFileWrite(writeRec, writeReq)
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("write sibling status = %d, want %d", writeRec.Code, http.StatusForbidden)
	}
	got, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "unchanged" {
		t.Fatalf("outside file changed to %q", got)
	}
}

func TestFilePanelBoundaryIgnoresAgentAllowAll(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(filepath.Dir(home), "outside.txt")
	prev := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(true)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prev) })

	srv := fileTestServer(home)
	body, err := json.Marshal(map[string]string{"path": outside, "content": "no"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/file/write", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleFileWrite(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write outside with allow-all status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file unexpectedly exists: %v", err)
	}
}

func TestFilePanelRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation requires Windows Developer Mode or elevation: %v", err)
		}
		t.Fatal(err)
	}

	srv := fileTestServer(home)
	req := httptest.NewRequest(http.MethodGet, "/api/file/read?path="+secretURLPath(filepath.Join(link, "secret.txt")), nil)
	rec := httptest.NewRecorder()
	srv.handleFileRead(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read through escaping symlink status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestFilePanelAllowsWorkspaceReadWrite(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "notes.txt")
	srv := fileTestServer(home)
	body, err := json.Marshal(map[string]string{"path": path, "content": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	writeReq := httptest.NewRequest(http.MethodPost, "/api/file/write", bytes.NewReader(body))
	writeRec := httptest.NewRecorder()
	srv.handleFileWrite(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write workspace status = %d: %s", writeRec.Code, writeRec.Body.String())
	}

	readReq := httptest.NewRequest(http.MethodGet, "/api/file/read?path="+secretURLPath(path), nil)
	readRec := httptest.NewRecorder()
	srv.handleFileRead(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read workspace status = %d: %s", readRec.Code, readRec.Body.String())
	}
}

func secretURLPath(path string) string {
	// Using a request URL here exercises the handler's real query decoding.
	return url.QueryEscape(path)
}
