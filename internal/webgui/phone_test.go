package webgui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/system/phonetunnel"
)

type phoneTestProcess struct {
	done chan struct{}
	once sync.Once
}

func (p *phoneTestProcess) Stderr() io.Reader {
	return strings.NewReader("https://test-phone.trycloudflare.com\n")
}
func (p *phoneTestProcess) Wait() error { <-p.done; return nil }
func (p *phoneTestProcess) Kill() error { p.once.Do(func() { close(p.done) }); return nil }
func setupTestPhone(t *testing.T, s *Server) {
	t.Helper()
	setupTestPhoneIdle(t, s, 24*time.Hour)
}
func setupTestPhoneIdle(t *testing.T, s *Server, idle time.Duration) {
	t.Helper()
	m := phonetunnel.New(t.TempDir())
	m.IdleTimeout = idle
	data := []byte("offline test executable")
	sum := sha256.Sum256(data)
	m.Installer = phonetunnel.Installer{DataDir: t.TempDir(), Platform: "windows/amd64", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
	if err := os.MkdirAll(filepath.Dir(m.Installer.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Installer.Path(), data, 0600); err != nil {
		t.Fatal(err)
	}
	m.Factory = func(path, target string) (phonetunnel.Process, error) {
		return &phoneTestProcess{done: make(chan struct{})}, nil
	}
	s.phone = &phoneAccess{manager: m, target: "http://127.0.0.1:1", sessions: make(map[[32]byte]uint64)}
	if err := m.Start(context.Background(), s.phone.target, false, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop("stop") })
	deadline := time.Now().Add(2 * time.Second)
	for m.Status().State != "on" {
		if time.Now().After(deadline) {
			t.Fatalf("tunnel not ready: %+v", m.Status())
		}
		time.Sleep(time.Millisecond)
	}
}
func phoneRequest(h http.Handler, method, host, path, body string, cookie *http.Cookie, proxy bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if proxy {
		r.Header.Set("Cf-Ray", "test")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func pairTestPhone(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	link, err := s.phone.pair()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	w := phoneRequest(s.withLocalGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })), "POST", u.Host, phonePage, `{"token":"`+u.Fragment+`"}`, nil, true)
	if w.Code != 200 {
		t.Fatalf("pair: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge != phoneCookieMaxAge {
		t.Fatalf("cookies=%+v", cookies)
	}
	return cookies[0]
}
func TestPhoneAccessTable(t *testing.T) {
	s := NewServer(nil, true)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	h := s.withLocalGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		name, host, path string
		cookie           *http.Cookie
		proxy            bool
		code             int
	}{
		{"local", "localhost", "/api/health", nil, false, 401},
		{"unpaired", "test-phone.trycloudflare.com", "/api/health", nil, true, 401},
		{"paired", "test-phone.trycloudflare.com", "/api/health", cookie, true, 204},
		{"wrong-host", "evil.test", "/api/health", cookie, true, 403},
		{"proxy-local-host", "localhost", "/api/health", cookie, true, 403},
		{"bootstrap", "test-phone.trycloudflare.com", localSessionBootstrap, cookie, true, 403},
		{"management", "test-phone.trycloudflare.com", "/api/phone/status", cookie, true, 403},
		{"secret-alias", "test-phone.trycloudflare.com", "/api/provider-key", cookie, true, 403},
		{"pair-page", "test-phone.trycloudflare.com", phonePage, nil, true, 200},
		// A paired phone returning to an already used QR link reopens the app.
		{"pair-page-paired", "test-phone.trycloudflare.com", phonePage, cookie, true, 303},
		{"unpaired-page", "test-phone.trycloudflare.com", "/", nil, true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := phoneRequest(h, "GET", tc.host, tc.path, "", tc.cookie, tc.proxy)
			if w.Code != tc.code {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.code, w.Body.String())
			}
		})
	}
	if w := phoneRequest(h, "GET", "test-phone.trycloudflare.com", "/", "", nil, true); !strings.Contains(w.Body.String(), "Nowy kod") || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("unpaired page: %q %q", w.Header().Get("Content-Type"), w.Body.String())
	}
	if w := phoneRequest(h, "GET", "test-phone.trycloudflare.com", phonePage, "", cookie, true); w.Header().Get("Location") != "/" {
		t.Fatalf("paired re-open location=%q", w.Header().Get("Location"))
	}
	secret := s.withLocalGuard(s.withSecretLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	if w := phoneRequest(secret, "GET", "test-phone.trycloudflare.com", "/api/provider/key/reveal", "", cookie, true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	s.phone.manager.Stop("stop")
	if w := phoneRequest(h, "GET", "test-phone.trycloudflare.com", "/api/health", "", cookie, true); w.Code != 401 {
		t.Fatal(w.Code)
	}
	s.allowRemote = false
	if w := phoneRequest(h, "GET", "test-phone.trycloudflare.com", "/api/health", "", cookie, true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := phoneRequest(h, "GET", "localhost", "/api/health", "", nil, false); w.Code != 204 {
		t.Fatal(w.Code)
	}
}
func TestPhonePairingOneUseExpiryRotationAndLimit(t *testing.T) {
	s := NewServer(nil, false)
	setupTestPhone(t, s)
	h := s.withLocalGuard(http.NotFoundHandler())
	old, _ := s.phone.pair()
	oldURL, _ := url.Parse(old)
	fresh, _ := s.phone.pair()
	u, _ := url.Parse(fresh)
	submit := func(token string) int {
		return phoneRequest(h, "POST", u.Host, phonePage, `{"token":"`+token+`"}`, nil, true).Code
	}
	if submit(oldURL.Fragment) != 403 {
		t.Fatal("old token survived rotation")
	}
	if submit(u.Fragment) != 200 || submit(u.Fragment) != 403 {
		t.Fatal("token not one-use")
	}
	fresh, _ = s.phone.pair()
	u, _ = url.Parse(fresh)
	s.phone.expires = time.Now().Add(-time.Second)
	if submit(u.Fragment) != 403 {
		t.Fatal("expired token accepted")
	}
	for range 7 {
		submit("wrong")
	}
	if submit("wrong") != 429 {
		t.Fatal("missing rate limit")
	}
}
func TestPhoneAPILocalOnlyAndStopRevokes(t *testing.T) {
	s := NewServer(nil, false)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	h := s.Handler()
	for _, path := range []string{"/api/phone/status", "/api/phone/start", "/api/phone/stop", "/api/phone/pair"} {
		if w := phoneRequest(h, "POST", "test-phone.trycloudflare.com", path, "{}", cookie, true); w.Code != 403 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := phoneRequest(h, "POST", "localhost", "/api/phone/pair", "{}", nil, false)
	var fields map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &fields) != nil || !strings.Contains(fields["qr_svg"], "<svg") {
		t.Fatalf("pair API: %d %s", w.Code, w.Body.String())
	}
	if w := phoneRequest(h, "POST", "localhost", "/api/phone/stop", "{}", nil, false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if len(s.phone.sessions) != 0 {
		t.Fatal("sessions survived stop")
	}
}
