package webgui

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/storage/session"
)

func portableUploadRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("session", "x"+session.PortableFileSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/session/import", &upload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Host = "127.0.0.1:8765"
	req.RemoteAddr = "127.0.0.1:12345"
	return req
}

func TestSessionPortableExportImportRoundTrip(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(srv.eng.Home(), "echo", "portable chat")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []session.Encoded{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi there"}} {
		if err := store.AppendMessage(context.Background(), created.ID, m); err != nil {
			t.Fatal(err)
		}
	}

	exported := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session/export?id="+created.ID, nil)
	req.Host = "127.0.0.1:8765"
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(exported, req)
	if exported.Code != http.StatusOK {
		t.Fatalf("export=%d %s", exported.Code, exported.Body.String())
	}
	if cd := exported.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, session.PortableFileSuffix) {
		t.Fatalf("Content-Disposition=%q", cd)
	}
	if cc := exported.Header().Get("Cache-Control"); cc != "no-store, private" {
		t.Fatalf("Cache-Control=%q", cc)
	}

	imported := httptest.NewRecorder()
	srv.Handler().ServeHTTP(imported, portableUploadRequest(t, exported.Body.Bytes()))
	if imported.Code != http.StatusOK {
		t.Fatalf("import=%d %s", imported.Code, imported.Body.String())
	}
	var out struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.ID == "" || out.ID == created.ID {
		t.Fatalf("import result=%+v (original %s)", out, created.ID)
	}
	orig, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	copySess, err := store.Get(out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copySess.MessageCount != orig.MessageCount || copySess.MessageCount != 2 {
		t.Fatalf("message count copy=%d orig=%d", copySess.MessageCount, orig.MessageCount)
	}
	if !sameSessionWorkspace(copySess.Cwd, srv.eng.Home()) {
		t.Fatalf("imported cwd=%q home=%q", copySess.Cwd, srv.eng.Home())
	}

	missing := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/session/export?id=does-not-exist", nil)
	req.Host = "127.0.0.1:8765"
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(missing, req)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown export=%d", missing.Code)
	}
}

func TestSessionPortableImportRejectsMalformed(t *testing.T) {
	srv := newTestServer(t, false)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, portableUploadRequest(t, []byte(`{"format":"nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed import=%d %s", rec.Code, rec.Body.String())
	}
}

func TestSessionImportStaysLoopbackOnlyWithAllowRemote(t *testing.T) {
	srv := newTestServer(t, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/session/import", nil)
	req.Host = "example.test"
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("Authorization", "Bearer "+srv.sessionToken)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote session import=%d, want 403", rec.Code)
	}
}
