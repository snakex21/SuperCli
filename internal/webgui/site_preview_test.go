package webgui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func previewRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:8123"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:42000"
	return r
}

func TestSitePreviewOnlyServesViewerWithoutEngine(t *testing.T) {
	handler, err := SitePreviewHandler("localhost:5173", "pl")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/app.css", "/preview.css", "/js/site-preview.js", "/js/preview-window.js", "/api/preview/state"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, previewRequest("GET", path, ""))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatal("viewer may be framed")
		}
		if path == "/api/preview/state" {
			var state struct {
				URL, Language string
				Copy          map[string]string
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.URL != "http://localhost:5173" || state.Language != "pl" || state.Copy["preview.title"] == "preview.title" || state.Copy["preview.title"] == "Site preview" || state.Copy["preview.clear"] != "Wyczyść podgląd" {
				t.Fatalf("state=%+v", state)
			}
		}
	}
	for _, path := range []string{"/api/chat", "/api/sessions", "/api/provider/key/reveal", "/locales/en.json", "/index.html"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, previewRequest("GET", path, ""))
		if rec.Code != 404 {
			t.Fatalf("viewer exposes %s: %d", path, rec.Code)
		}
	}
	if _, err := SitePreviewHandler("javascript:alert(1)", "en"); err == nil {
		t.Fatal("unsafe initial target accepted")
	}
}

func TestSiteBrowserHandlersValidateAndOpenOnlyOnRequest(t *testing.T) {
	var opened []string
	opener := func(url string) error { opened = append(opened, url); return nil }
	for _, tc := range []struct {
		method, path, body string
		open               bool
		want               int
	}{
		{"POST", "/api/browser/resolve", `{"url":"localhost:5173/demo?x=1&y=2"}`, false, 200},
		{"POST", "/api/browser/open", `{"url":"example.com/demo?x=1&y=2"}`, true, 200},
		{"GET", "/api/browser/open", "", true, 405},
		{"POST", "/api/browser/open", `{"url":"javascript:alert(1)"}`, true, 400},
		{"POST", "/api/browser/open", `{"url":"file:///C:/Windows"}`, true, 400},
		{"POST", "/api/browser/open", `{"url":"http://127.0.0.1:8123/api/health"}`, true, 400},
		{"POST", "/api/browser/open", `{"url":"http://user:secret@example.com"}`, true, 400},
		{"POST", "/api/browser/open", "bad JSON", true, 400},
		{"POST", "/api/browser/open", strings.Repeat("x", 9000), true, 400},
	} {
		rec := httptest.NewRecorder()
		siteBrowserHandler(tc.open, opener).ServeHTTP(rec, previewRequest(tc.method, tc.path, tc.body))
		if rec.Code != tc.want {
			t.Fatalf("%s %s %s: %d %s", tc.method, tc.path, tc.body, rec.Code, rec.Body.String())
		}
	}
	if len(opened) != 1 || opened[0] != "https://example.com/demo?x=1&y=2" {
		t.Fatalf("opens=%v", opened)
	}
	rec := httptest.NewRecorder()
	siteBrowserHandler(true, func(string) error { return errors.New("failed") }).ServeHTTP(rec, previewRequest("POST", "/api/browser/open", `{"url":"https://example.com"}`))
	if rec.Code != 500 || strings.TrimSpace(rec.Body.String()) != "failed" {
		t.Fatalf("native launch diagnostic lost: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewIsolationPreservesPDFButRejectsAppFramesAndForeignAPIs(t *testing.T) {
	handler := (&Server{}).withLocalGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		path, origin, site, dest, peer string
		want                           int
		framePolicy                    string
	}{
		{"/", "", "same-origin", "document", "", 204, "DENY"},
		{"/api/browser/open", "http://127.0.0.1:8123", "same-origin", "empty", "", 204, "DENY"},
		{"/", "", "same-site", "iframe", "", 403, "DENY"},
		{"/", "", "same-origin", "iframe", "", 403, "DENY"},
		{"/js/00-helpers.js", "", "same-origin", "iframe", "", 403, "DENY"},
		{"/api/attachment/preview?path=demo.pdf", "", "same-origin", "iframe", "", 204, "SAMEORIGIN"},
		{"/api/attachment/preview?path=demo.pdf", "", "same-site", "iframe", "", 403, "SAMEORIGIN"},
		{"/api/browser/open", "http://localhost:5173", "same-site", "empty", "", 403, "DENY"},
		{"/api/chat", "null", "cross-site", "empty", "", 403, "DENY"},
		{"/api/chat", "", "cross-site", "empty", "", 403, "DENY"},
		{"/api/health", "", "none", "empty", "", 204, "DENY"},
		{"/api/browser/open", "", "same-origin", "empty", "192.0.2.3:80", 403, ""},
	} {
		t.Run(tc.path+"-"+tc.origin+"-"+tc.site+"-"+tc.dest+"-"+tc.peer, func(t *testing.T) {
			r := previewRequest("GET", tc.path, "")
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("Sec-Fetch-Dest", tc.dest)
			if tc.peer != "" {
				r.RemoteAddr = tc.peer
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Fatalf("code=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Header().Get("X-Frame-Options") != tc.framePolicy {
				t.Fatal(rec.Header())
			}
		})
	}
}
