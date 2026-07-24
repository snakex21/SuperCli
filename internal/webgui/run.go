package webgui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

// errNoBrowser is returned by OpenAppWindow when no Chromium-family
// browser supporting --app mode could be located.
var errNoBrowser = errors.New("webgui: no Chromium app-mode browser found")

// RunOptions configures a full GUI launch via Run.
type RunOptions struct {
	// Addr is the listen address. Empty means "127.0.0.1:0" (an
	// OS-assigned free port on loopback).
	Addr string
	// AllowRemote disables the loopback-only guard. Default false.
	AllowRemote bool
	// NoWindow skips opening a browser window (server only).
	NoWindow bool
	// UIRoot serves a host application's front-end instead of the embedded UI.
	UIRoot string
	// UIFS serves an application front-end embedded in the executable. UIRoot
	// takes precedence when both are set.
	UIFS fs.FS
	// AppName controls the native window title and Windows application identity.
	AppName string
	// IconPath optionally replaces the executable icon in the native window.
	// Branded launchers use it while keeping the engine binary replaceable.
	IconPath string
}

// Run starts the web GUI server on a loopback port, opens an
// app-mode window pointed at it, and blocks until the process is
// interrupted (Ctrl+C), the server stops, or the app-mode window is
// closed. It is the one call a thin main() needs.
//
// The listener is created before the window opens so the URL always
// resolves to a live server (no race, no fixed-port collision).
func Run(eng *Engine, opts RunOptions) error {
	defer eng.Close()
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("webgui.Run: listen: %w", err)
	}
	baseURL := localLaunchURL(ln.Addr().String())

	webServer := NewServer(eng, opts.AllowRemote)
	url := webServer.bootstrapLaunchURL(baseURL)
	appName := strings.TrimSpace(opts.AppName)
	if appName == "" {
		appName = "SuperCli"
	}
	webServer.appName = appName
	if opts.UIRoot != "" {
		if err := webServer.UseUIRoot(opts.UIRoot); err != nil {
			_ = ln.Close()
			return fmt.Errorf("webgui.Run: custom UI: %w", err)
		}
	} else if opts.UIFS != nil {
		if err := webServer.UseUIFS(opts.UIFS); err != nil {
			_ = ln.Close()
			return fmt.Errorf("webgui.Run: embedded custom UI: %w", err)
		}
	}
	if opts.AllowRemote {
		tokenPath, removeToken, tokenErr := writeRemoteSessionTokenFile(eng.DataDir(), webServer.sessionToken)
		if tokenErr != nil {
			_ = ln.Close()
			return fmt.Errorf("webgui.Run: remote authentication: %w", tokenErr)
		}
		defer removeToken()
		log.Printf("%s remote sign-in: username supercli; password is in %s", appName, tokenPath)
	}
	srv := &http.Server{
		Handler:           webServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	log.Printf("%s web GUI: %s", appName, url)
	windowClosedCh := make(chan struct{}, 1)
	if !opts.NoWindow {
		// Prefer a true native WebView2 host. Besides giving the program its own
		// Windows identity, this avoids tying server lifetime to a Chrome/Edge
		// launcher process. The browser path remains a compatibility fallback
		// for machines without the WebView2 runtime.
		if nativeErr := runNativeAppWindow(url, appName, eng.DataDir(), opts.IconPath, eng.HasActiveWork); nativeErr == nil {
			log.Printf("native app window closed; shutting down…")
			return shutdownAfterWindowClose(srv)
		} else {
			log.Printf("native app window unavailable (%v); trying browser app mode", nativeErr)
		}
		profileDir := filepath.Join(eng.DataDir(), "browser-profile")
		if mkErr := os.MkdirAll(profileDir, 0o700); mkErr != nil {
			log.Printf("could not prepare isolated app profile: %v", mkErr)
			profileDir = ""
		}
		if appCmd, werr := OpenAppWindow(url, profileDir); werr != nil {
			log.Printf("app-mode window unavailable (%v); opening default browser", werr)
			if berr := OpenInBrowser(url); berr != nil {
				log.Printf("could not open a browser automatically: %v — open %s manually", berr, url)
			}
		} else if appCmd != nil {
			go func() {
				_ = appCmd.Wait()
				windowClosedCh <- struct{}{}
			}()
		}
	}

	// Wait for Ctrl+C / SIGTERM or a fatal serve error.
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	select {
	case <-ctx.Done():
		log.Printf("shutting down…")
		return Shutdown(srv)
	case <-windowClosedCh:
		log.Printf("app window closed; shutting down…")
		return shutdownAfterWindowClose(srv)
	case serveErr := <-errCh:
		return serveErr
	}
}

// Closing the desktop window is an explicit request to exit, so there is no
// value in leaving an invisible process around for the full generic shutdown
// timeout. Give ordinary requests a short grace period, then close remaining
// browser/SSE connections. Ctrl+C and server-only mode still use Shutdown's
// longer graceful timeout.
func shutdownAfterWindowClose(srv *http.Server) error {
	const windowGrace = 350 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), windowGrace)
	defer cancel()
	err := srv.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if closeErr := srv.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		return closeErr
	}
	return nil
}

// localLaunchURL turns a wildcard listener address into a loopback URL for
// the automatically opened local window. A browser cannot use 0.0.0.0 or ::
// as a trustworthy local Host header, even when the listener accepts it.
func localLaunchURL(listenerAddr string) string {
	host, port, err := net.SplitHostPort(listenerAddr)
	if err != nil {
		return "http://" + listenerAddr + "/"
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}
