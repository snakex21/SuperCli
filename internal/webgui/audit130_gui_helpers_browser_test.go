package webgui

// audit-130: shared helpers for the full-app headless Chrome UX audit tests.
// The real GUI server (echo provider, isolated temp data dir) is served on a
// loopback httptest port and driven only through DOM events evaluated in our
// own headless browser (internal/testbrowser). No paid calls, no coordinate
// clicks, no user browser profile.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/testbrowser"
)

type audit130App struct {
	t       *testing.T
	ctx     context.Context
	srv     *Server
	api     *httptest.Server
	browser *testbrowser.Browser
	page    *testbrowser.Page
	dataDir string
}

// audit130StartApp serves the complete embedded GUI (index.html and every
// script) from the real handler and opens it in headless Chrome.
func audit130StartApp(t *testing.T, width, height int) *audit130App {
	t.Helper()
	return audit130StartAppWith(t, width, height, nil)
}

// audit130StartAppWith lets a test point the engine at an in-process mock
// provider. Home and data dir are a fresh t.TempDir, never the user's data.
func audit130StartAppWith(t *testing.T, width, height int, mutate func(*config.Config)) *audit130App {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	t.Cleanup(cancel)
	cfg := echoConfig()
	if mutate != nil {
		mutate(&cfg)
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	eng, err := NewEngine(cfg, dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	srv := NewServer(eng, false)
	api := httptest.NewServer(srv.Handler()) // 127.0.0.1, free port
	t.Cleanup(api.Close)
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	page, err := browser.NewPage(ctx, api.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	// Targets open in the background, where requestAnimationFrame never
	// fires; a screenshot brings our own tab to the front first.
	if _, err := page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	app := &audit130App{t: t, ctx: ctx, srv: srv, api: api, browser: browser, page: page, dataDir: dir}
	app.viewport(width, height)
	app.waitReady()
	return app
}

func (a *audit130App) viewport(width, height int) {
	a.t.Helper()
	if err := a.page.SetViewport(a.ctx, width, height); err != nil {
		a.t.Fatal(err)
	}
}

// eval runs an expression (it may return a promise) and decodes its value.
func (a *audit130App) eval(expr string, out any) {
	a.t.Helper()
	if err := a.page.Eval(a.ctx, expr, out); err != nil {
		a.t.Fatalf("eval: %v", err)
	}
}

// waitReady waits until the boot sequence rendered the shell and settings.
func (a *audit130App) waitReady() {
	a.t.Helper()
	var ok bool
	a.eval(`(async()=>{for(let i=0;i<200;i++){if(typeof ui==='object'&&typeof applyUI==='function'&&document.querySelector('#side-tabs button')&&typeof I18N==='object'&&I18N.en)return true;await new Promise(r=>setTimeout(r,50));}return false;})()`, &ok)
	if !ok {
		a.t.Fatal("GUI did not finish booting")
	}
	a.settle()
}

func (a *audit130App) settle() {
	a.t.Helper()
	a.eval(`new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>setTimeout(()=>r(true),60))))`, nil)
}

// reload navigates the same tab to the app again (a fresh page load).
func (a *audit130App) reload() {
	a.t.Helper()
	if err := a.page.Navigate(a.ctx, a.api.URL+"/"); err != nil {
		a.t.Fatal(err)
	}
	a.waitReady()
}

// setLanguage switches the UI language the way the appearance select does
// (load the catalog, apply, persist) without needing the panel open.
func (a *audit130App) setLanguage(code string) {
	a.t.Helper()
	var got string
	a.eval(`(async()=>{ui.lang=`+jsString(code)+`;await loadLanguage(ui.lang);applyUI();if(typeof saveUI==='function')saveUI();return document.documentElement.lang;})()`, &got)
	if got != code {
		a.t.Fatalf("language switch to %s: html lang=%q", code, got)
	}
	a.settle()
}

// shot saves a screenshot when SUPERCLI_AUDIT_SHOTS names a directory.
func (a *audit130App) shot(name string) string {
	a.t.Helper()
	dir := os.Getenv("SUPERCLI_AUDIT_SHOTS")
	if dir == "" {
		return ""
	}
	png, err := a.page.Screenshot(a.ctx)
	if err != nil {
		a.t.Logf("screenshot %s: %v", name, err)
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.t.Logf("screenshot dir: %v", err)
		return ""
	}
	path := filepath.Join(dir, name+".png")
	if err := os.WriteFile(path, png, 0o644); err != nil {
		a.t.Logf("screenshot %s: %v", name, err)
		return ""
	}
	a.t.Logf("screenshot: %s", path)
	return path
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func audit130Join(lines []string) string { return strings.Join(lines, "\n  ") }
