package webgui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func seedWebSession(t *testing.T, srv *Server, cwd, title string) string {
	t.Helper()
	store, err := session.OpenStore(srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(cwd, "test-model", title)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewWriter(store, sess.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "first prompt"}); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func TestSessionsRenameAndDelete(t *testing.T) {
	srv := newTestServer(t, false)
	id := seedWebSession(t, srv, srv.eng.Home(), "Old name")

	rename := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rename, localProviderJSONRequest(http.MethodPatch, "/api/sessions", `{"id":"`+id+`","title":"  New name  "}`))
	if rename.Code != http.StatusOK {
		t.Fatalf("rename status = %d; body = %s", rename.Code, rename.Body.String())
	}
	rows, err := srv.eng.listSessions(context.Background(), 10)
	if err != nil || len(rows) != 1 || rows[0].FirstUserMsg != "New name" {
		t.Fatalf("sessions after rename = %+v, err=%v", rows, err)
	}

	remove := httptest.NewRecorder()
	srv.Handler().ServeHTTP(remove, localProviderRequest(http.MethodDelete, "/api/sessions?id="+id))
	if remove.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body = %s", remove.Code, remove.Body.String())
	}
	rows, err = srv.eng.listSessions(context.Background(), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("sessions after delete = %+v, err=%v", rows, err)
	}
}

func TestSessionMutationsRejectOtherWorkspace(t *testing.T) {
	srv := newTestServer(t, false)
	foreign := filepath.Join(t.TempDir(), "other-project")
	id := seedWebSession(t, srv, foreign, "Foreign")

	for _, tc := range []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"rename", http.MethodPatch, "/api/sessions", `{"id":"` + id + `","title":"Changed"}`},
		{"delete", http.MethodDelete, "/api/sessions?id=" + id, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var req *http.Request
			if tc.body != "" {
				req = localProviderJSONRequest(tc.method, tc.target, tc.body)
			} else {
				req = localProviderRequest(tc.method, tc.target)
			}
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "active project") {
				t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSessionRenameValidation(t *testing.T) {
	srv := newTestServer(t, false)
	id := seedWebSession(t, srv, srv.eng.Home(), "Original")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPatch, "/api/sessions", `{"id":"`+id+`","title":"   "}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
}
