// Package shuffler provides IP rotation for HTTP clients by
// cycling through a pool of proxy servers. Designed to bypass
// rate limits on free-tier AI APIs (e.g. Kilo).
package shuffler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultProxySources are public proxy lists tried by AutoConfigure().
// Each is fetched in order; the first successful response with >0 proxies wins.
var DefaultProxySources = []string{
	"https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/http.txt",
	"https://api.proxyscrape.com/v2/?request=getproxies&protocol=http&timeout=10000&country=all",
	"https://www.proxy-list.download/api/v1/get?type=http",
}

// Shuffler manages a pool of proxy URLs and rotates through them.
// It is safe for concurrent use.
type Shuffler struct {
	mu       sync.RWMutex
	proxies  []string      // proxy URLs (http://host:port or socks5://host:port)
	current  int           // index of the active proxy
	interval time.Duration // auto-rotation interval (0 = disabled)
	enabled  bool          // master on/off switch
	stopCh   chan struct{} // signals the background rotator to stop
}

// New returns a fresh Shuffler with no proxies and rotation disabled.
func New() *Shuffler {
	return &Shuffler{
		stopCh: make(chan struct{}),
	}
}

// AddProxy appends a proxy URL to the pool. The URL must be a valid
// HTTP or SOCKS5 proxy (e.g. "http://1.2.3.4:8080" or "socks5://...).
// Duplicates are silently ignored.
func (s *Shuffler) AddProxy(proxyURL string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" || strings.HasPrefix(proxyURL, "#") {
		return nil
	}
	// Normalize: ensure scheme is present.
	if !strings.Contains(proxyURL, "://") {
		proxyURL = "http://" + proxyURL
	}
	// Validate.
	if _, err := url.Parse(proxyURL); err != nil {
		return fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.proxies {
		if p == proxyURL {
			return nil // duplicate
		}
	}
	s.proxies = append(s.proxies, proxyURL)
	return nil
}

// LoadFromURL fetches a proxy list from a remote URL (one proxy per
// line, # for comments) and adds them to the pool.
func (s *Shuffler) LoadFromURL(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d loading proxy list from %s", resp.StatusCode, u)
	}
	return s.loadFromReader(resp.Body)
}

// LoadFromFile reads a proxy list from a local file (one proxy per
// line, # for comments) and adds them to the pool.
func (s *Shuffler) LoadFromFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.loadFromReader(f)
}

// Enable starts IP shuffling. When rotation is active and interval > 0,
// the Shuffler automatically advances to the next proxy.
func (s *Shuffler) Enable() {
	s.mu.Lock()
	s.enabled = true
	s.mu.Unlock()
}

// Disable stops IP shuffling. Requests go out directly (no proxy).
func (s *Shuffler) Disable() {
	s.mu.Lock()
	s.enabled = false
	s.mu.Unlock()
}

// IsEnabled reports whether shuffling is active.
func (s *Shuffler) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// SetInterval configures the auto-rotation period. A value <= 0
// disables automatic rotation (manual Rotate() still works).
// Minimum is 60 seconds to avoid hammering the API.
func (s *Shuffler) SetInterval(d time.Duration) {
	if d > 0 && d < 60*time.Second {
		d = 60 * time.Second
	}
	s.mu.Lock()
	old := s.interval
	s.interval = d
	s.mu.Unlock()
	if old == 0 && d > 0 && s.enabled {
		go s.rotateLoop()
	}
}

// Rotate advances to the next proxy immediately. Returns the new
// proxy URL or "" if the pool is empty.
func (s *Shuffler) Rotate() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.proxies) == 0 {
		return ""
	}
	s.current = (s.current + 1) % len(s.proxies)
	return s.proxies[s.current]
}

// GetCurrentProxy returns the active proxy URL, or "" when the pool
// is empty or shuffling is disabled.
func (s *Shuffler) GetCurrentProxy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled || len(s.proxies) == 0 {
		return ""
	}
	return s.proxies[s.current]
}

// List returns a copy of all proxy URLs in the pool.
func (s *Shuffler) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.proxies))
	copy(out, s.proxies)
	return out
}

// Status returns a human-readable summary.
func (s *Shuffler) Status() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return "IP shuffler: OFF"
	}
	total := len(s.proxies)
	if total == 0 {
		return "IP shuffler: ON (no proxies configured)"
	}
	active := s.current + 1 // 1-based for display
	interval := s.interval
	if interval > 0 {
		return fmt.Sprintf("IP shuffler: ON  proxy %d/%d  rotate every %s", active, total, interval)
	}
	return fmt.Sprintf("IP shuffler: ON  proxy %d/%d  (manual rotation)", active, total)
}

// CheckProxies tests each proxy by making a request to a test URL.
// Returns a list of (proxy, error) pairs.
func (s *Shuffler) CheckProxies(ctx context.Context, testURL string) []ProxyStatus {
	if testURL == "" {
		testURL = "https://api.kilo.ai/api/openrouter/models"
	}
	s.mu.RLock()
	proxies := make([]string, len(s.proxies))
	copy(proxies, s.proxies)
	s.mu.RUnlock()

	results := make([]ProxyStatus, len(proxies))
	var wg sync.WaitGroup
	for i, p := range proxies {
		wg.Add(1)
		go func(i int, proxy string) {
			defer wg.Done()
			proxyURL, err := url.Parse(proxy)
			if err != nil {
				results[i] = ProxyStatus{Proxy: proxy, OK: false, Err: err.Error()}
				return
			}
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
			client := &http.Client{
				Transport: transport,
				Timeout:   10 * time.Second,
			}
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
			if reqErr != nil {
				results[i] = ProxyStatus{Proxy: proxy, OK: false, Err: reqErr.Error()}
				return
			}
			req.Header.Set("Authorization", "Bearer anonymous")
			resp, doErr := client.Do(req)
			if doErr != nil {
				results[i] = ProxyStatus{Proxy: proxy, OK: false, Err: doErr.Error()}
				return
			}
			resp.Body.Close()
			results[i] = ProxyStatus{Proxy: proxy, OK: resp.StatusCode < 400}
		}(i, p)
	}
	wg.Wait()
	return results
}

// RemoveBroken removes all proxies that failed the last check.
func (s *Shuffler) RemoveBroken(statuses []ProxyStatus) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	broken := make(map[string]bool)
	for _, st := range statuses {
		if !st.OK {
			broken[st.Proxy] = true
		}
	}
	var kept []string
	for _, p := range s.proxies {
		if !broken[p] {
			kept = append(kept, p)
		}
	}
	removed := len(s.proxies) - len(kept)
	s.proxies = kept
	if s.current >= len(s.proxies) && len(s.proxies) > 0 {
		s.current = 0
	}
	return removed
}

// HTTPClient returns an *http.Client that routes requests through the
// active proxy (when shuffling is enabled and a proxy is available).
// When shuffling is off or the pool is empty, requests go direct.
func (s *Shuffler) HTTPClient() *http.Client {
	return &http.Client{
		Transport: &shufflerTransport{s: s},
		Timeout:   120 * time.Second,
	}
}

// ProxyStatus reports the result of a proxy check.
type ProxyStatus struct {
	Proxy string
	OK    bool
	Err   string
}

// --- internal ---

func (s *Shuffler) loadFromReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	var added int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := s.AddProxy(line); err != nil {
			return fmt.Errorf("line %q: %w", line, err)
		}
		added++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if added == 0 {
		return fmt.Errorf("no valid proxy URLs found")
	}
	return nil
}

func (s *Shuffler) rotateLoop() {
	s.mu.RLock()
	interval := s.interval
	s.mu.RUnlock()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.RLock()
			enabled := s.enabled
			s.mu.RUnlock()
			if enabled {
				s.Rotate()
			}
		case <-s.stopCh:
			return
		}
	}
}

// shufflerTransport is an http.RoundTripper that dynamically selects
// the proxy based on the Shuffler's current state.
type shufflerTransport struct {
	s    *Shuffler
	base http.RoundTripper
}

func (t *shufflerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	proxy := t.s.GetCurrentProxy()
	if proxy == "" {
		// No proxy — use direct transport.
		if t.base == nil {
			t.base = http.DefaultTransport
		}
		return t.base.RoundTrip(req)
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return nil, fmt.Errorf("shuffler: bad proxy URL %q: %w", proxy, err)
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	return transport.RoundTrip(req)
}

// Global is the process-wide Shuffler instance used by the Kilo
// provider integration. Commands like /shuffle operate on this
// instance.
var Global = New()

// Count returns the number of proxies in the pool.
func (s *Shuffler) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.proxies)
}

// AutoConfigure fetches proxy lists from DefaultProxySources, checks
// which proxies actually work, removes broken ones, enables the
// shuffler, and starts auto-rotation at the given interval.
//
// It is designed to be called via /shuffle auto — the user does not
// need to know any proxy URLs.
//
// Returns a summary string suitable for display in the TUI.
func (s *Shuffler) AutoConfigure(ctx context.Context, interval time.Duration) string {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	var loaded int
	for _, src := range DefaultProxySources {
		if err := s.LoadFromURL(ctx, src); err != nil {
			continue
		}
		loaded = s.Count()
		if loaded > 0 {
			break
		}
	}
	if loaded == 0 {
		return "IP shuffler: no public proxies available.\nTry adding one manually:\n  /shuffle add http://1.2.3.4:8080"
	}

	// Check which proxies work.
	statuses := s.CheckProxies(ctx, "")
	working := 0
	for _, st := range statuses {
		if st.OK {
			working++
		}
	}
	s.RemoveBroken(statuses)

	if s.Count() == 0 {
		return fmt.Sprintf("IP shuffler: loaded %d proxies but none are working.\nTry again later or add manually:\n  /shuffle add http://1.2.3.4:8080", loaded)
	}

	s.SetInterval(interval)
	s.Enable()
	return fmt.Sprintf("IP shuffler: loaded %d proxies, %d working, %d kept.\nAuto-rotation every %s.\n%s",
		loaded, working, s.Count(), interval, s.Status())
}
