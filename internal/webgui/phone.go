package webgui

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"supercli/internal/system/phonetunnel"
	"supercli/internal/webgui/qrcode"
)

const phonePage = "/.__supercli/phone"
const phoneCookie = "supercli_phone_session"

// phoneCookieMaxAge keeps a paired phone usable after its tab or browser is
// closed. Server-side validity still ends with the tunnel generation.
const phoneCookieMaxAge = 7 * 24 * 60 * 60

type phoneAccess struct {
	manager    *phonetunnel.Manager
	target     string
	mu         sync.Mutex
	token      [32]byte
	expires    time.Time
	generation uint64
	attempts   int
	window     time.Time
	sessions   map[[32]byte]uint64
	onPair     func(string)
}

func (s *Server) initPhone(target string) {
	s.phone = &phoneAccess{manager: phonetunnel.New(s.eng.DataDir()), target: target, sessions: make(map[[32]byte]uint64)}
}
func isPhoneProxy(r *http.Request) bool {
	// Presence, including an empty value, must not turn a proxy into a local peer.
	_, ray := r.Header[http.CanonicalHeaderKey("Cf-Ray")]
	_, ip := r.Header[http.CanonicalHeaderKey("Cf-Connecting-Ip")]
	return ray || ip
}
func isLocalRequest(r *http.Request) bool {
	return isLoopbackHost(r.Host) && isLoopbackRemoteAddr(r.RemoteAddr) && !isPhoneProxy(r)
}
func (p *phoneAccess) valid(r *http.Request, generation uint64) bool {
	cookie, err := r.Cookie(phoneCookie)
	if err != nil {
		return false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions[hash] == generation && generation != 0
}
func (p *phoneAccess) pair() (string, error) {
	st := p.manager.Status()
	if st.State != "on" {
		return "", errors.New("phone tunnel is not active")
	}
	token := newSessionToken()
	p.mu.Lock()
	if p.generation != st.Generation {
		p.sessions = make(map[[32]byte]uint64)
	}
	p.token = sha256.Sum256([]byte(token))
	p.expires = time.Now().Add(10 * time.Minute)
	p.generation = st.Generation
	url := "https://" + st.Host + phonePage + "#" + token
	if p.onPair != nil {
		p.onPair(url)
	}
	p.mu.Unlock()
	return url, nil
}
func (s *Server) phoneGuard(w http.ResponseWriter, r *http.Request, next http.Handler) (handled bool) {
	if s.phone == nil {
		return false
	}
	st := s.phone.manager.Status()
	if st.State != "on" {
		return false
	}
	if isLocalRequest(r) {
		return false
	}
	if r.Host != st.Host || !isLoopbackRemoteAddr(r.RemoteAddr) {
		http.Error(w, "forbidden: unexpected tunnel host", http.StatusForbidden)
		return true
	}
	if r.URL.Path == localSessionBootstrap || strings.HasPrefix(r.URL.Path, "/api/phone/") || r.URL.Path == "/api/provider-key" {
		http.Error(w, "forbidden: local-only endpoint", http.StatusForbidden)
		return true
	}
	if !guardPreviewOrigin(w, r) {
		return true
	}
	paired := s.phone.valid(r, st.Generation)
	if r.URL.Path == phonePage {
		if paired && r.Method == http.MethodGet {
			// Re-scanning an already used code on a paired phone just reopens the app.
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return true
		}
		s.handlePhonePairing(w, r)
		return true
	}
	if !paired {
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "unauthorized: pair this phone from the PC", http.StatusUnauthorized)
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><title>SuperCli</title></head><body style="font-family:sans-serif;padding:24px;line-height:1.5"><h2>SuperCli</h2><p>Ten telefon nie jest połączony. Na PC otwórz „Dostęp z telefonu”, kliknij „Nowy kod” i zeskanuj QR.</p><p>This phone is not paired. On the PC open phone access, press “New code” and scan the QR.</p></body></html>`)
		return true
	}
	next.ServeHTTP(w, r)
	return true
}

func (s *Server) handlePhonePairing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet {
		nonce := newSessionToken()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><title>SuperCli</title></head><body><p id="status">SuperCli — pairing / parowanie…</p><script nonce="%s">(async function(){var token=location.hash.slice(1);history.replaceState(null,"",location.pathname);try{var r=await fetch(location.pathname,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token:token})});if(!r.ok)throw new Error("Pairing expired or rejected. Show a new code on the PC. / Pokaż nowy kod na PC.");location.replace("/");}catch(e){document.getElementById("status").textContent=e.message;}})();</script></body></html>`, nonce)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if origin := r.Header.Get("Origin"); (origin != "" && origin != "https://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "cross-origin pairing request", http.StatusForbidden)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	p := s.phone
	st := p.manager.Status()
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if now.Sub(p.window) >= time.Minute {
		p.window = now
		p.attempts = 0
	}
	p.attempts++
	if p.attempts > 10 {
		http.Error(w, "too many pairing attempts; wait one minute", 429)
		return
	}
	hash := sha256.Sum256([]byte(req.Token))
	if subtle.ConstantTimeCompare(hash[:], p.token[:]) != 1 || !now.Before(p.expires) || p.generation != st.Generation || st.State != "on" {
		http.Error(w, "pairing token expired or invalid", 403)
		return
	}
	if len(p.sessions) >= 32 {
		http.Error(w, "phone session limit reached; disconnect from the PC", 429)
		return
	}
	p.token = [32]byte{}
	p.expires = time.Time{}
	session := newSessionToken()
	p.sessions[sha256.Sum256([]byte(session))] = st.Generation
	http.SetCookie(w, &http.Cookie{Name: phoneCookie, Value: session, Path: "/", MaxAge: phoneCookieMaxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePhoneStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	writeJSON(w, struct {
		phonetunnel.Status
		Available bool   `json:"available"`
		Size      int64  `json:"download_size"`
		Source    string `json:"source"`
	}{s.phone.manager.Status(), s.phone.manager.Available(), phonetunnel.DownloadSize, phonetunnel.DownloadURL})
}
func (s *Server) handlePhoneStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		AllowDownload bool `json:"allow_download"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if !req.AllowDownload && !s.phone.manager.Available() {
		http.Error(w, "download consent required", http.StatusConflict)
		return
	}
	if err := s.phone.manager.Start(context.Background(), s.phone.target, req.AllowDownload, nil); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, s.phone.manager.Status())
}
func (s *Server) handlePhoneStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.phone.manager.Stop("stop")
	s.phone.mu.Lock()
	s.phone.sessions = make(map[[32]byte]uint64)
	s.phone.token = [32]byte{}
	s.phone.expires = time.Time{}
	s.phone.mu.Unlock()
	writeJSON(w, s.phone.manager.Status())
}
func (s *Server) handlePhonePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	url, err := s.phone.pair()
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	svg, err := qrcode.SVG(url, 6)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"url": url, "qr_svg": svg})
}
