package webgui

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPhoneSyncGuardAndPromptOnlyIdleRenewal(t *testing.T) {
	s := newTestServer(t, false)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	h := s.Handler()
	const host = "test-phone.trycloudflare.com"
	expiry := s.phone.manager.Status().ExpiresAt
	assertUnchanged := func() {
		t.Helper()
		if !s.phone.manager.Status().ExpiresAt.Equal(expiry) {
			t.Fatal("non-prompt request renewed expiry")
		}
	}
	for _, path := range []string{"/api/sync", "/api/sync/ws", "/api/sync/stop"} {
		method := "GET"
		if path == "/api/sync/stop" {
			method = "POST"
		}
		if w := phoneRequest(h, method, host, path, `{}`, nil, true); w.Code != 401 {
			t.Fatalf("unpaired %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/health", "/api/sessions", "/"} {
		if w := phoneRequest(h, "GET", host, path, "", cookie, true); w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	phoneRequest(h, "POST", host, "/api/chat", `{"prompt":""}`, cookie, true)
	phoneRequest(h, "POST", host, "/api/chat", `{"prompt":"no cookie"}`, nil, true)
	phoneRequest(h, "POST", "localhost", "/api/chat", `{"prompt":"PC prompt"}`, nil, false)
	assertUnchanged()
	api := httptest.NewServer(h)
	defer api.Close()
	observer := openSyncWS(t, api, "", cookie)
	readSyncUntil(t, observer, "snapshot")
	defer observer.close()
	assertUnchanged()
	conn, r := dialSyncTestWS(t, api, "/api/chat/ws", cookie)
	defer conn.Close()
	assertUnchanged()
	_, err := conn.Write(maskedFrame(1, true, []byte(`{"prompt":"phone prompt"}`)))
	if err != nil {
		t.Fatal(err)
	}
	for {
		op, _, err := readServerFrame(r)
		if err != nil {
			t.Fatal(err)
		}
		if op == 8 {
			break
		}
	}
	next := s.phone.manager.Status().ExpiresAt
	if !next.After(expiry) || next.Before(time.Now().Add(24*time.Hour-time.Minute)) {
		t.Fatal("WS prompt did not renew 24h")
	}
	expiry = next
	phoneRequest(h, "POST", host, "/api/chat", `{"prompt":"HTTP phone prompt"}`, cookie, true)
	if !s.phone.manager.Status().ExpiresAt.After(expiry) {
		t.Fatal("HTTP prompt did not renew")
	}
	expiry = s.phone.manager.Status().ExpiresAt
	req, _ := http.NewRequest("GET", api.URL+"/api/sync", nil)
	req.Host = host
	req.Header.Set("Cf-Ray", "test")
	req.AddCookie(cookie)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		t.Fatal("no phone SSE snapshot")
	}
	assertUnchanged()
	s.phone.manager.Stop("stop")
	closed := false
	for i := 0; i < 100; i++ {
		if _, err := observer.read(); err != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("phone WS survived revocation")
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if w := phoneRequest(h, "POST", host, "/api/sync/stop", `{}`, cookie, true); w.Code == 200 {
		t.Fatal("revoked cookie accepted")
	}
	// Reconnection traffic is not a user message, even with an old cookie.
	assertUnchanged()
}
