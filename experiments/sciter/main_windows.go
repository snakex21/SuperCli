//go:build windows

// This launcher is an opt-in compatibility experiment, separate from supercli-web.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/crgimenes/glaze"
	"supercli/internal/system/config"
	"supercli/internal/webgui"
)

//go:embed assets/*
var trialAssets embed.FS

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	runtime.LockOSThread()
	smoke := flag.Bool("smoke", false, "run isolated echo/streaming smoke tests, print metrics, then close")
	compare := flag.Bool("compare-webview", false, "run the same smoke test with the existing WebView2 host")
	flag.Parse()
	if *compare {
		*smoke = true
	}
	exe, err := os.Executable()
	must(err)
	root := filepath.Dir(exe)
	data := filepath.Join(root, "trial-data")
	if *smoke {
		data, err = os.MkdirTemp(root, "smoke-data-")
		must(err)
	}
	home := filepath.Join(data, "workspace")
	must(os.MkdirAll(home, 0700))
	must(os.Chdir(home))
	_, err = config.EnsureLanguage(data, home, "en")
	must(err)
	cfg := config.Config{Provider: config.ProviderEcho, Model: "echo-test", BaseURL: "http://localhost"}
	must(cfg.Normalize())
	configPath := filepath.Join(data, "config.toml")
	stored, err := config.LoadToml(configPath)
	must(err)
	haveEcho := false
	for _, provider := range stored.Providers {
		if provider.Name == "sciter-echo" {
			haveEcho = true
		}
	}
	if !haveEcho {
		stored.Providers = append(stored.Providers, config.ProviderConf{Name: "sciter-echo", Type: config.ProviderEcho, BaseURL: "http://localhost", Model: "echo-test", CachedModels: []string{"echo-test"}})
		must(config.SaveToml(configPath, stored))
	}
	engine, err := webgui.NewEngine(cfg, home, data)
	must(err)
	defer engine.Close()
	engine.SetExplicitEcho(true)
	backend := webgui.NewServer(engine, false).Handler()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	origin := "http://" + listener.Addr().String()
	uiDir := filepath.Join(data, "ui")
	must(os.MkdirAll(uiDir, 0700))
	original := getAsset(backend, origin, "/")
	assetRE := regexp.MustCompile("(?:src|href)=\"([^\"]+)\"")
	for _, match := range assetRE.FindAllSubmatch(original, -1) {
		route := string(match[1])
		if strings.HasPrefix(route, "data:") || strings.HasPrefix(route, "#") {
			continue
		}
		name := strings.TrimPrefix(route, "/")
		if name == ".__supercli/ui/runtime.js" {
			name = "shared-ui/runtime.js"
		}
		if strings.Contains(name, "..") || strings.Contains(name, ":") {
			panic("unexpected asset path")
		}
		content := getAsset(backend, origin, "/"+strings.TrimPrefix(route, "/"))
		if name == "js/01-i18n.js" {
			content = bytes.ReplaceAll(content, []byte("document.documentElement.lang = I18N[language] ? language : \"en\";"), []byte("document.documentElement.setAttribute(\"lang\", I18N[language] ? language : \"en\");"))
			content = bytes.ReplaceAll(content, []byte("document.documentElement.dir = \"ltr\";"), []byte("document.documentElement.setAttribute(\"dir\", \"ltr\");"))
		}
		destination := filepath.Join(uiDir, filepath.FromSlash(name))
		must(os.MkdirAll(filepath.Dir(destination), 0700))
		must(os.WriteFile(destination, content, 0600))
	}
	// Copy the provider logos referenced dynamically by the unchanged UI.
	icons := getAsset(backend, origin, "/icons/providers/")
	iconRE := regexp.MustCompile("href=\"([a-zA-Z0-9_-]+\\.svg)\"")
	for _, match := range iconRE.FindAllSubmatch(icons, -1) {
		name := string(match[1])
		dest := filepath.Join(uiDir, "icons", "providers", name)
		must(os.MkdirAll(filepath.Dir(dest), 0700))
		must(os.WriteFile(dest, getAsset(backend, origin, "/icons/providers/"+name), 0600))
	}
	// Settings and history remain in this launcher's adjacent data folder.
	if *smoke {
		localRequest(backend, origin, "POST", "/api/settings", []byte("{\"ui.lang\":\"en\"}"))
	}
	script, err := trialAssets.ReadFile("assets/adapter.js")
	must(err)
	script = bytes.ReplaceAll(script, []byte("TRIAL_ORIGIN"), []byte(origin))
	must(os.WriteFile(filepath.Join(uiDir, "adapter.js"), script, 0600))
	css, err := trialAssets.ReadFile("assets/adapter.css")
	must(err)
	must(os.WriteFile(filepath.Join(uiDir, "adapter.css"), css, 0600))
	testing, err := trialAssets.ReadFile("assets/smoke.js")
	must(err)
	html := string(original)
	if *compare {
		html = strings.Replace(html, "</head>", "<script>window.trialErrors=[];window.addEventListener('error',e=>trialErrors.push(e.message));window.addEventListener('unhandledrejection',e=>trialErrors.push(String(e.reason)));</script></head>", 1)
	} else {
		html = strings.Replace(html, "<html lang=\"en\">", "<html lang=\"en\" window-width=\"1200px\" window-height=\"820px\">", 1)
		html = strings.Replace(html, "src=\"/.__supercli/ui/runtime.js\"", "src=\"shared-ui/runtime.js\"", 1)
		html = strings.Replace(html, "</head>", "<script type=\"module\" src=\"adapter.js\"></script><link rel=\"stylesheet\" href=\"adapter.css\" /></head>", 1)
	}
	attribution := "This Application uses Sciter Engine (http://sciter.com), copyright Terra Informatica Software, Inc."
	html = strings.Replace(html, "</body>", "<footer id=\"sciter-attribution\" style=\"position:fixed;bottom:0;left:8px;font-size:9px;color:#68686f;\">"+attribution+"</footer></body>", 1)
	if !*compare {
		html = strings.Replace(html, "</body>", "<script type=\"module\">document.$('#send-btn').on('click',e=>{e.preventDefault();document.$('#composer').requestSubmit()});</script></body>", 1)
	}
	if *smoke {
		tag := "<script>"
		if !*compare {
			tag = "<script type=\"module\">"
		}
		html = strings.Replace(html, "</body>", tag+string(testing)+"</script></body>", 1)
	}
	must(os.WriteFile(filepath.Join(uiDir, "index.html"), []byte(html), 0600))
	reports := make(chan json.RawMessage, 1)
	capture := make(chan struct{})
	snapshotDone := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			if *compare {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(html))
				return
			}
		case "/.__trial/report":
			if !*smoke {
				http.NotFound(w, r)
				return
			}
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			select {
			case reports <- b:
			default:
			}
			w.WriteHeader(200)
			return
		case "/.__trial/capture":
			if !*smoke {
				http.NotFound(w, r)
				return
			}
			select {
			case <-capture:
				w.WriteHeader(200)
			case <-r.Context().Done():
			}
			return
		case "/.__trial/snapshot":
			if !*smoke {
				http.NotFound(w, r)
				return
			}
			b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err = os.WriteFile(filepath.Join(data, "preview.png"), b, 0600); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			select {
			case <-snapshotDone:
			default:
				close(snapshotDone)
			}
			w.WriteHeader(200)
			return
		case "/.__trial/stream", "/.__trial/slow":
			if !*smoke {
				http.NotFound(w, r)
				return
			}
			serveStreamFixture(w, r)
			return
		}
		backend.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	started := time.Now()
	if *compare {
		profile := filepath.Join(data, "browser-profile")
		must(os.MkdirAll(profile, 0700))
		must(os.Setenv("APPDATA", profile))
		syscallDPI()
		view, err := glaze.New(false)
		must(err)
		defer view.Destroy()
		view.SetTitle("SuperCli — WebView2 comparison")
		width, height := comparisonWindowSize()
		view.SetSize(width, height, glaze.HintNone)
		go func() {
			r := awaitReport(reports)
			before := processTreeMetrics(os.Getpid())
			time.Sleep(4 * time.Second)
			after := processTreeMetrics(os.Getpid())
			printReport(data, "WebView2", r, started, before, after)
			closeProcessWindow(os.Getpid())
		}()
		view.Navigate(origin + "/")
		showComparisonWindow(uintptr(view.Window()))
		view.Run()
		return
	}
	runtimePath := filepath.Join(root, "sciter-runtime", "scapp.exe")
	if _, err := os.Stat(runtimePath); err != nil {
		panic("Run experiments/sciter/build.ps1 to fetch the pinned runtime: " + err.Error())
	}
	cmd := exec.Command(runtimePath, filepath.Join(uiDir, "index.html"))
	cmd.Dir = uiDir
	cmd.Env = os.Environ()
	for _, key := range []string{"APPDATA", "LOCALAPPDATA", "USERPROFILE", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "TEMP", "TMP"} {
		dir := filepath.Join(data, "portable", strings.ToLower(key))
		must(os.MkdirAll(dir, 0700))
		cmd.Env = append(cmd.Env, key+"="+dir)
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	must(cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	fmt.Fprintln(os.Stderr, "Sciter trial: echo model; isolated data:", data)
	if !*smoke {
		must(<-done)
		return
	}
	var result json.RawMessage
	select {
	case result = <-reports:
	case err := <-done:
		panic(fmt.Sprintf("Sciter exited before report: %v", err))
	case <-time.After(40 * time.Second):
		cmd.Process.Kill()
		<-done
		panic("Sciter smoke test timed out")
	}
	before := processTreeMetrics(os.Getpid())
	time.Sleep(4 * time.Second)
	after := processTreeMetrics(os.Getpid())
	printReport(data, "Sciter Direct2D", result, started, before, after)
	close(capture)
	select {
	case <-snapshotDone:
	case <-time.After(5 * time.Second):
		fmt.Fprintln(os.Stderr, "Snapshot not available")
	}
	closeProcessWindow(cmd.Process.Pid)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	var testResult struct{ Passed bool }
	must(json.Unmarshal(result, &testResult))
	if !testResult.Passed {
		os.Exit(1)
	}
}

func localRequest(handler http.Handler, origin, method, route string, body []byte) []byte {
	r := httptest.NewRequest(method, origin+route, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	if len(body) > 0 {
		r.Header.Set("Content-Type", "application/json")
	}
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, r)
	if out.Code != 200 {
		panic(fmt.Sprintf("asset %s: HTTP %d: %s", route, out.Code, out.Body.String()))
	}
	return out.Body.Bytes()
}
func getAsset(handler http.Handler, origin, route string) []byte {
	return localRequest(handler, origin, "GET", route, nil)
}
func awaitReport(reports <-chan json.RawMessage) json.RawMessage {
	select {
	case r := <-reports:
		return r
	case <-time.After(40 * time.Second):
		panic("UI smoke test timed out")
	}
}
func printReport(data, variant string, result json.RawMessage, started time.Time, before, after metrics) {
	report := map[string]any{"variant": variant, "result": result, "elapsed_ms": time.Since(started).Milliseconds(), "process_tree": after, "idle_cpu_ms_over_4s": after.CPUMS - before.CPUMS}
	b, err := json.MarshalIndent(report, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(data, "result.json"), b, 0600))
	fmt.Println(string(b))
}
func serveStreamFixture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	flush := w.(http.Flusher)
	if r.URL.Path == "/.__trial/slow" {
		w.Write([]byte("data: {\"type\":\"message\",\"text\":\"started\"}\n\n"))
		flush.Flush()
		<-r.Context().Done()
		return
	}
	text := []byte("data: {\"type\":\"message\",\"text\":\"Zażółć 🦊\"}\r\n\r\n")
	// Deliberately divide a UTF-8 character and the SSE frame between HTTP chunks.
	split := bytes.Index(text, []byte("ż")) + 1
	w.Write(text[:split])
	flush.Flush()
	time.Sleep(80 * time.Millisecond)
	w.Write(text[split:])
	flush.Flush()
	select {
	case <-time.After(350 * time.Millisecond):
	case <-r.Context().Done():
		return
	}
	w.Write([]byte("data: {\"type\":\"done\"}\n\n"))
	flush.Flush()
}
