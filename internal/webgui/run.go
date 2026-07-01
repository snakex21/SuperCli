package webgui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
}

// Run starts the web GUI server on a loopback port, opens an
// app-mode window pointed at it, and blocks until the process is
// interrupted (Ctrl+C), the server stops, or the app-mode window is
// closed. It is the one call a thin main() needs.
//
// The listener is created before the window opens so the URL always
// resolves to a live server (no race, no fixed-port collision).
func Run(eng *Engine, opts RunOptions) error {
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("webgui.Run: listen: %w", err)
	}
	url := "http://" + ln.Addr().String() + "/"

	srv := &http.Server{
		Handler:           NewServer(eng, opts.AllowRemote).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	log.Printf("SuperCli web GUI: %s", url)
	windowClosedCh := make(chan struct{}, 1)
	if !opts.NoWindow {
		if appCmd, werr := OpenAppWindow(url); werr != nil {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-ctx.Done():
		log.Printf("shutting down…")
		return Shutdown(srv)
	case <-windowClosedCh:
		log.Printf("app window closed; shutting down…")
		return Shutdown(srv)
	case serveErr := <-errCh:
		return serveErr
	}
}
