package webgui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPhoneProxyHeadersFailClosed(t *testing.T) {
	for _, header := range []string{"Cf-Ray", "Cf-Connecting-Ip"} {
		for _, secret := range []bool{false, true} {
			t.Run(header+map[bool]string{true: "/secret", false: "/guard"}[secret], func(t *testing.T) {
				s := NewServer(nil, false)
				next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
				var h http.Handler = s.withLocalGuard(next)
				if secret {
					h = s.withSecretLocalOnly(next)
				}
				r := httptest.NewRequest("GET", "http://localhost/api/health", nil)
				r.RemoteAddr = "127.0.0.1:1234"
				r.Header.Set(header, "probe")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status=%d, want 403", w.Code)
				}
			})
		}
	}
}
