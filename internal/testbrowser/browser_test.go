package testbrowser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The only non-browser executable used by these lifecycle tests is this test's
// own Go binary. No application/user executable or existing profile is opened.
func TestMain(m *testing.M) {
	if mode := os.Getenv("SUPERCLI_TESTBROWSER_HELPER"); mode != "" && slices.Contains(os.Args, "--headless") {
		switch mode {
		case "exit":
			fmt.Fprintln(os.Stderr, "synthetic browser early exit")
			os.Exit(42)
		case "remote":
			fmt.Fprintln(os.Stdout, "DevTools listening on ws://192.0.2.1:123/devtools/browser/unsafe")
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBrowserStartupFailuresCleanOwnedProfile(t *testing.T) {
	for _, mode := range []string{"exit", "silent", "remote"} {
		t.Run(mode, func(t *testing.T) {
			temporary := t.TempDir()
			for _, name := range []string{"TMP", "TEMP", "TMPDIR"} {
				t.Setenv(name, temporary)
			}
			t.Setenv("SUPERCLI_TESTBROWSER_HELPER", mode)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			timeout := 3 * time.Second
			if mode == "silent" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			start := time.Now()
			browser, err := Start(ctx, Options{Executable: executable})
			if err == nil {
				browser.Close()
				t.Fatal("accepted failing browser helper")
			}
			if mode == "exit" && !strings.Contains(err.Error(), "synthetic browser early exit") {
				t.Fatalf("missing early-exit diagnostics: %v", err)
			}
			if mode == "silent" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("missing startup timeout: %v", err)
			}
			if mode == "remote" && !strings.Contains(err.Error(), "loopback") {
				t.Fatalf("unsafe CDP endpoint was not rejected: %v", err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("startup failure did not return promptly")
			}
			entries, err := os.ReadDir(temporary)
			if err != nil || len(entries) != 0 {
				t.Fatalf("owned profile leaked: %v, %v", entries, err)
			}
		})
	}
}

func TestBrowserOptionsAndExecutable(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Start(ctx, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	if _, err := Start(t.Context(), Options{Width: -1}); err == nil {
		t.Fatal("accepted invalid viewport")
	}
	missing := filepath.Join(t.TempDir(), "not-a-browser")
	t.Setenv("SUPERCLI_TEST_BROWSER", missing)
	if _, err := findExecutable(""); err == nil || !strings.Contains(err.Error(), "SUPERCLI_TEST_BROWSER") || !strings.Contains(err.Error(), "dev-only") {
		t.Fatalf("missing actionable browser error: %v", err)
	}
	ownBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if path, err := findExecutable(ownBinary); err != nil || path != ownBinary {
		t.Fatalf("explicit executable must override environment: %q %v", path, err)
	}
}

func TestBrowserHeadless(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><head><title>Native DOM</title></head><body><main id=marker>%s</main></body></html>", r.URL.Path)
	}))
	defer server.Close()
	browser, err := Start(ctx, Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err) // missing Chrome is deliberately not skipped
	}
	defer func() {
		if err := browser.Close(); err != nil {
			t.Error(err)
		}
	}()
	profile := browser.profileDir
	if info, err := os.Stat(profile); err != nil || !info.IsDir() || !strings.HasPrefix(filepath.Base(profile), "supercli-testbrowser-") {
		t.Fatalf("missing isolated profile %q: %v", profile, err)
	}
	page, err := browser.NewPage(ctx, server.URL+"/first")
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Answer        int
		NativeDOM     bool
		Width, Height int
		GC            bool
		Heap          bool
	}
	expression := `(async () => {
		const button = document.createElement('button');
		button.textContent = 'native'; document.body.append(button);
		let clicked = false; button.addEventListener('click', () => { clicked = true; });
		button.click();
		await new Promise(resolve => setTimeout(resolve, 10));
		return {answer: 42, nativeDOM: clicked && button instanceof HTMLButtonElement,
			width: innerWidth, height: innerHeight, gc: typeof gc === 'function',
			heap: performance.memory.totalJSHeapSize > 0};
	})()`
	if err := page.Eval(ctx, expression, &value); err != nil {
		t.Fatal(err)
	}
	if value.Answer != 42 || !value.NativeDOM || value.Width != 640 || value.Height != 480 || !value.GC || !value.Heap {
		t.Fatalf("headless native DOM/awaitPromise/viewport/memory: %+v", value)
	}
	if err := page.SetViewport(ctx, 800, 600); err != nil {
		t.Fatal(err)
	}
	if err := page.SetViewport(ctx, 0, 600); err == nil {
		t.Fatal("accepted invalid viewport")
	}
	var viewport []int
	if err := page.Eval(ctx, "[innerWidth, innerHeight]", &viewport); err != nil || !slices.Equal(viewport, []int{800, 600}) {
		t.Fatalf("resized viewport %v: %v", viewport, err)
	}
	image, err := page.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(image))
	if err != nil || config.Width != 800 || config.Height != 600 {
		t.Fatalf("PNG screenshot dimensions %+v: %v", config, err)
	}
	err = page.Eval(ctx, "function smokeThrow(){throw new Error('headless-test-exception')} smokeThrow();\n//# sourceURL=testbrowser-smoke.js", nil)
	if err == nil || !strings.Contains(err.Error(), "headless-test-exception") || !strings.Contains(err.Error(), "smokeThrow") || !strings.Contains(err.Error(), "testbrowser-smoke.js") {
		t.Fatalf("JavaScript exception/stack was hidden: %v", err)
	}
	if err := page.Eval(ctx, "Promise.reject(new Error('async-test-exception'))", nil); err == nil || !strings.Contains(err.Error(), "async-test-exception") {
		t.Fatalf("promise rejection was hidden: %v", err)
	}
	if err := page.Eval(ctx, "undefined", nil); err != nil {
		t.Fatal(err)
	}
	var null any = "old"
	if err := page.Eval(ctx, "null", &null); err != nil || null != nil {
		t.Fatalf("JSON null %v: %v", null, err)
	}
	if err := page.Eval(ctx, "Infinity", &null); err == nil {
		t.Fatal("silently decoded non-JSON Infinity")
	}
	pending, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if err := page.Eval(pending, "new Promise(() => {})", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("evaluate cancellation: %v", err)
	}
	if err := page.Navigate(ctx, server.URL+"/second"); err != nil {
		t.Fatal(err)
	}
	if err := page.Navigate(ctx, server.URL+"/second#same-document"); err != nil {
		t.Fatal(err)
	}
	var marker string
	if err := page.Eval(ctx, "document.querySelector('#marker').textContent", &marker); err != nil || marker != "/second" {
		t.Fatalf("navigation did not await native DOM: %q %v", marker, err)
	}
	if err := browser.Close(); err != nil {
		t.Fatal(err)
	}
	if browser.cmd.ProcessState == nil || !browser.cmd.ProcessState.Success() {
		t.Fatalf("browser was not closed gracefully: %v", browser.cmd.ProcessState)
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile was not cleaned: %v", err)
	}
	t.Logf("real headless browser: native DOM, promises, exception stacks, viewport, %d-byte PNG, GC/memory, navigation, graceful exit and profile cleanup passed", len(image))
}
