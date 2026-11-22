package webgui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A damaged sessions.db does not stop the GUI: the conversation store opens
// on a fresh database, the damaged copy is kept, and /api/health reports it
// so the window tells the user where the old data is (1.0.39).
func TestGUIReportsRecoveredDatabase(t *testing.T) {
	dir := t.TempDir()
	damaged := bytes.Repeat([]byte("not sqlite "), 512)
	if err := os.WriteFile(filepath.Join(dir, "sessions.db"), damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if _, err := eng.sessionStore(); err != nil {
		t.Fatalf("damaged database blocked the conversation store: %v", err)
	}
	srv := NewServer(eng, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host, req.RemoteAddr = "127.0.0.1", "127.0.0.1:5000"
	srv.Handler().ServeHTTP(rec, req)
	var health struct {
		Recoveries []struct{ File, Backup string } `json:"storage_recoveries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("health: %v %s", err, rec.Body.String())
	}
	want := filepath.Join(dir, "sessions.db")
	for _, r := range health.Recoveries {
		if r.File == want {
			kept, err := os.ReadFile(r.Backup)
			if err != nil || !bytes.Equal(kept, damaged) {
				t.Fatalf("damaged copy not kept at %s: %v", r.Backup, err)
			}
			return
		}
	}
	t.Fatalf("health does not report the recovery: %s", rec.Body.String())
}
