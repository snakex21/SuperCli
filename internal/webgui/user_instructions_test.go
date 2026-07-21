package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserInstructionsAPIStoresPresetsWithoutContentLimit(t *testing.T) {
	srv := newTestServer(t, false)
	content := strings.Repeat("Pisz po polsku. ", 2000)
	body := `{"enabled":true,"active_id":"polish","presets":[{"id":"polish","name":"Po polsku","content":` + mustJSONText(t, content) + `}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/user-instructions", strings.NewReader(body))
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/user-instructions", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", rec.Code, rec.Body.String())
	}
	var got userInstructionsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.ActiveID != "polish" || got.Presets[0].Content != content {
		t.Fatalf("unexpected state: %#v", got.UserInstructionsState)
	}
	if got.EstimatedTokens <= 0 || !strings.HasSuffix(got.Path, "user-instructions.json") {
		t.Fatalf("missing metadata: %#v", got)
	}
}

func mustJSONText(t *testing.T, value string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
