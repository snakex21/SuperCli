package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

func TestProviderRenameEndpointUpdatesReferences(t *testing.T) {
	srv := newTestServer(t, false)
	seedProviderSecret(t, srv)
	if err := srv.eng.providerManager().Add("other", "openai", "https://other.test/v1", "", "x"); err != nil {
		t.Fatal(err)
	}
	global, _ := config.FindTomlPaths(srv.eng.dataDir, srv.eng.Home())
	tc, err := config.LoadToml(global)
	if err != nil {
		t.Fatal(err)
	}
	tc.DefaultProvider = "secret-provider"
	tc.TaskModel = "secret-provider/model"
	if err := config.SaveToml(global, tc); err != nil {
		t.Fatal(err)
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "model", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRuntime(sess.ID, "secret-provider", "model", ""); err != nil {
		t.Fatal(err)
	}
	if err := saveLastModel(srv.eng.dataDir, "model", "secret-provider"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/provider/rename", `{"name":"secret-provider","new_name":"Renamed One"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), providerTestSecret) {
		t.Fatal("rename response exposed the API key")
	}
	m := srv.eng.providerManager()
	if key, ok := m.APIKey("Renamed One"); !ok || key != providerTestSecret {
		t.Fatal("API key was not moved to the new name")
	}
	if _, ok := m.APIKey("secret-provider"); ok {
		t.Fatal("old provider name still configured")
	}
	tc, _ = config.LoadToml(global)
	if tc.DefaultProvider != "Renamed One" || tc.TaskModel != "Renamed One/model" {
		t.Fatalf("config references = %q, %q", tc.DefaultProvider, tc.TaskModel)
	}
	if got, _ := store.Get(sess.ID); got.Provider != "Renamed One" {
		t.Fatalf("session provider = %q", got.Provider)
	}
	if _, provider := LastModel(srv.eng.dataDir); provider != "Renamed One" {
		t.Fatalf("web GUI last model provider = %q", provider)
	}

	for _, body := range []string{
		`{"name":"Renamed One","new_name":"OTHER"}`,
		`{"name":"Renamed One","new_name":"a/b"}`,
		`{"name":"missing","new_name":"x"}`,
		`{"name":"Renamed One","new_name":""}`,
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localProviderJSONRequest(http.MethodPost, "/api/provider/rename", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, rec.Code)
		}
	}
	if _, ok := srv.eng.providerManager().APIKey("Renamed One"); !ok {
		t.Fatal("rejected renames changed the provider")
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localProviderRequest(http.MethodGet, "/api/provider/rename"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", rec.Code)
	}
}
