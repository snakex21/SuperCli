package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func boolp(v bool) *bool { return &v }

// TestSlotCache_CloudNeverBuilt: public endpoints must not even get a
// SlotCache — no probe, no request, nothing.
func TestSlotCache_CloudNeverBuilt(t *testing.T) {
	for _, u := range []string{
		"https://api.openai.com/v1",
		"https://openrouter.ai/api/v1",
		"https://example.com:8080/v1",
	} {
		if c := NewSlotCache(u, nil); c != nil {
			t.Errorf("NewSlotCache(%q) = %v, want nil for cloud host", u, c)
		}
	}
}

// TestSlotCache_OverrideWinsBothWays: slot_cache=true forces it on for
// a public address (self-hosted llama.cpp), false forces it off even
// for localhost.
func TestSlotCache_OverrideWinsBothWays(t *testing.T) {
	if c := NewSlotCache("https://example.com/v1", boolp(true)); c == nil {
		t.Error("explicit slot_cache=true must build a SlotCache for a public host")
	}
	if c := NewSlotCache("http://127.0.0.1:8089/v1", boolp(false)); c != nil {
		t.Error("explicit slot_cache=false must disable even for localhost")
	}
}

// TestSlotCache_SaveRestoreWire: save/restore hit the server-root
// /slots/0 endpoint (NOT under /v1) with the session-derived filename,
// and parse n_saved / n_restored.
func TestSlotCache_SaveRestoreWire(t *testing.T) {
	var gotPath, gotQuery, gotFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		var body struct {
			Filename string `json:"filename"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotFilename = body.Filename
		switch r.URL.Query().Get("action") {
		case "save":
			w.Write([]byte(`{"id_slot":0,"filename":"x","n_saved":1745,"n_written":14309796}`))
		case "restore":
			w.Write([]byte(`{"id_slot":0,"filename":"x","n_restored":1745,"n_read":14309796}`))
		case "erase":
			w.Write([]byte(`{"id_slot":0,"n_erased":1745}`))
		}
	}))
	defer srv.Close()

	c := NewSlotCache(srv.URL+"/v1", nil) // httptest listens on 127.0.0.1 -> auto-local
	if c == nil {
		t.Fatal("SlotCache not built for loopback server")
	}
	n, err := c.Save(context.Background(), "sess-123")
	if err != nil || n != 1745 {
		t.Fatalf("Save = (%d, %v), want (1745, nil)", n, err)
	}
	if gotPath != "/slots/0" {
		t.Errorf("path = %q, want /slots/0 at server root (the /v1 prefix must be stripped)", gotPath)
	}
	if gotQuery != "action=save" {
		t.Errorf("query = %q, want action=save", gotQuery)
	}
	if gotFilename != "supercli-sess-123.bin" {
		t.Errorf("filename = %q, want supercli-sess-123.bin", gotFilename)
	}

	n, err = c.Restore(context.Background(), "sess-123")
	if err != nil || n != 1745 {
		t.Fatalf("Restore = (%d, %v), want (1745, nil)", n, err)
	}
	if gotQuery != "action=restore" {
		t.Errorf("query = %q, want action=restore", gotQuery)
	}
	if err := c.Erase(context.Background()); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if c.Disabled() {
		t.Error("healthy SlotCache must stay enabled")
	}
}

// TestSlotCache_SilentOffAfter501: a server without --slot-save-path
// answers 501; the first call errors (loggable), every later call is a
// silent no-op that never touches the network again.
func TestSlotCache_SilentOffAfter501(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(`{"error":{"code":501,"message":"This server does not support slots action. Start it with --slot-save-path","type":"not_supported_error"}}`))
	}))
	defer srv.Close()

	c := NewSlotCache(srv.URL+"/v1", nil)
	if _, err := c.Save(context.Background(), "sess-1"); err == nil {
		t.Fatal("first Save against 501 must return the probe error")
	}
	if !c.Disabled() {
		t.Fatal("SlotCache must disable itself after 501")
	}
	for i := 0; i < 3; i++ {
		if n, err := c.Restore(context.Background(), "sess-1"); n != 0 || err != nil {
			t.Fatalf("post-disable Restore = (%d, %v), want silent (0, nil)", n, err)
		}
		if n, err := c.Save(context.Background(), "sess-1"); n != 0 || err != nil {
			t.Fatalf("post-disable Save = (%d, %v), want silent (0, nil)", n, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want exactly 1 (zero retries after the probe)", got)
	}
}

// TestSlotCache_TransportErrorDisables: a dead host disables on first
// use, same contract as 501.
func TestSlotCache_TransportErrorDisables(t *testing.T) {
	c := NewSlotCache("http://127.0.0.1:1/v1", nil) // port 1: nothing listens
	if _, err := c.Restore(context.Background(), "s"); err == nil {
		t.Fatal("Restore against a dead host must error once")
	}
	if !c.Disabled() {
		t.Fatal("SlotCache must disable after a transport error")
	}
}

// TestSlotCache_NilReceiverIsSilent: call sites stay unconditional.
func TestSlotCache_NilReceiverIsSilent(t *testing.T) {
	var c *SlotCache
	if n, err := c.Save(context.Background(), "s"); n != 0 || err != nil {
		t.Fatalf("nil Save = (%d, %v), want (0, nil)", n, err)
	}
	if !c.Disabled() {
		t.Error("nil SlotCache must report Disabled")
	}
}

// TestSlotFilename_Sanitized: llama.cpp rejects path separators and
// oddball characters (fs_validate_filename); everything outside the
// safe set collapses to '-'.
func TestSlotFilename_Sanitized(t *testing.T) {
	got := SlotFilename(`sess/..\1:2 ż`)
	if strings.ContainsAny(got, `/\: `) || strings.Contains(got, "ż") {
		t.Errorf("unsafe characters survived: %q", got)
	}
	if !strings.HasPrefix(got, "supercli-") || !strings.HasSuffix(got, ".bin") {
		t.Errorf("filename shape wrong: %q", got)
	}
	if SlotFilename("sess-42") != "supercli-sess-42.bin" {
		t.Errorf("plain id mangled: %q", SlotFilename("sess-42"))
	}
}
