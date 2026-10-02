package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/system/browser"
	"supercli/internal/system/uilang"
)

// Preview sites may run their own JavaScript, but cannot frame this app or
// submit cross-origin requests to its native actions and agent APIs.
func guardPreviewOrigin(w http.ResponseWriter, r *http.Request) bool {
	documentPreview := r.Method == http.MethodGet && r.URL.Path == "/api/attachment/preview"
	if documentPreview {
		// The existing PDF viewer frames only MIME-checked attachment bytes.
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); (dest == "iframe" || dest == "frame") && !(documentPreview && r.Header.Get("Sec-Fetch-Site") == "same-origin") {
		http.Error(w, "application cannot be framed", http.StatusForbidden)
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.EqualFold(parsed.Host, r.Host) {
			http.Error(w, "cross-origin application request", http.StatusForbidden)
			return false
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "cross-site application request", http.StatusForbidden)
		return false
	}
	return true
}

func siteBrowserHandler(open bool, opener func(string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
			http.Error(w, "invalid URL request", http.StatusBadRequest)
			return
		}
		target, err := browser.NormalizeURL(body.URL)
		if err != nil {
			http.Error(w, "invalid HTTP(S) URL", http.StatusBadRequest)
			return
		}
		parsed, _ := url.Parse(target)
		if strings.EqualFold(parsed.Host, r.Host) {
			http.Error(w, "the application itself cannot be previewed", http.StatusBadRequest)
			return
		}
		if open {
			if err := opener(target); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, map[string]any{"url": target, "ok": true})
	}
}

// SitePreviewHandler serves only the viewer and URL actions. It never creates
// an Engine, loads a model, opens the session database, or probes providers.
func SitePreviewHandler(target, language string) (http.Handler, error) {
	normalized, err := browser.NormalizeURL(target)
	if err != nil {
		return nil, err
	}
	language = uilang.Resolve(language)
	copy := make(map[string]string)
	for _, key := range []string{"title", "address", "open", "browser", "reload", "clear", "hint", "invalid", "opened", "failed", "close"} {
		copy["preview."+key] = uilang.Text(language, "preview."+key)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/preview/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"url": normalized, "language": language, "copy": copy})
	})
	mux.HandleFunc("/api/browser/resolve", siteBrowserHandler(false, nil))
	mux.HandleFunc("/api/browser/open", siteBrowserHandler(true, browser.Open))
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return nil, err
	}
	static := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			page, readErr := assetsFS.ReadFile("assets/preview.html")
			if readErr != nil {
				http.Error(w, "preview unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(page)
		case "/app.css", "/preview.css", "/js/site-preview.js", "/js/preview-window.js":
			static.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	return (&Server{}).withLocalGuard(mux), nil
}

// RunSitePreview creates a portable window only on demand. Closing the window
// shuts down this small server; it is independent of the running TUI agent.
func RunSitePreview(target, language, dataDir string, opts RunOptions) error {
	handler, err := SitePreviewHandler(target, language)
	if err != nil {
		return err
	}
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	if !isLoopbackHost(addr) {
		return fmt.Errorf("site preview requires a loopback listen address")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	defer srv.Close()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	launchURL := localLaunchURL(ln.Addr().String())
	title := "SuperCli · " + uilang.Text(language, "preview.title")
	var closed <-chan struct{}
	if opts.NoWindow {
		fmt.Printf("%s: %s\n", title, launchURL)
	} else {
		profile := filepath.Join(dataDir, "site-preview")
		if err := runNativeAppWindow(launchURL, title, profile, opts.IconPath, func() bool { return false }); err == nil {
			return shutdownAfterWindowClose(srv)
		}
		cmd, err := OpenAppWindow(launchURL, filepath.Join(profile, "browser-profile"))
		if err != nil {
			return fmt.Errorf("open portable preview: %w", err)
		}
		windowClosed := make(chan struct{})
		closed = windowClosed
		go func() { _ = cmd.Wait(); close(windowClosed) }()
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	select {
	case <-closed:
		return shutdownAfterWindowClose(srv)
	case <-ctx.Done():
		return Shutdown(srv)
	case err := <-errCh:
		return err
	}
}
