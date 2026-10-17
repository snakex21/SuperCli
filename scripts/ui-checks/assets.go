package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed js/*.js
var scripts embed.FS

const bootstrap = `<script src="/__ui_checks/bootstrap.js"></script>`

func injectHTML(data []byte) []byte {
	if bytes.Contains(data, []byte("<head>")) {
		return bytes.Replace(data, []byte("<head>"), []byte("<head>"+bootstrap), 1)
	}
	return append([]byte(bootstrap), data...)
}

// Only immutable source assets are exposed; no repository/user FileServer.
func assetSnapshot(root string) (map[string][]byte, error) {
	assets := map[string][]byte{}
	for _, tree := range []string{"assets", "shared-ui"} {
		dir, err := os.OpenRoot(filepath.Join(root, "internal", "webgui", tree))
		if err != nil {
			return nil, err
		}
		err = fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if name != "." && strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !entry.Type().IsRegular() {
				return nil
			}
			switch path.Ext(name) {
			case ".html", ".js", ".css", ".json", ".svg":
			default:
				return nil
			}
			file, err := dir.Open(name)
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(io.LimitReader(file, 8<<20+1))
			closeErr := file.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return err
			}
			if len(data) > 8<<20 {
				return fmt.Errorf("asset too large: %s", name)
			}
			key := "/" + name
			if tree == "shared-ui" {
				key = "/.__supercli/ui/" + name
			}
			if key == "/index.html" {
				key = "/"
				data = injectHTML(data)
			}
			if key == "/js/12-keyboard-init.js" {
				data = bytes.Replace(data, []byte("(async function init() {"), []byte("window.__uiCheckInit = (async function init() {"), 1)
			}
			assets[key] = data
			return nil
		})
		closeErr := dir.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return nil, err
		}
	}
	languageSource, err := os.ReadFile(filepath.Join(root, "internal", "system", "uilang", "language.go"))
	if err != nil {
		return nil, err
	}
	matches := regexp.MustCompile(`\{"([\w-]+)", "([^"]+)", "(ltr|rtl)"\}`).FindAllSubmatch(languageSource, -1)
	if len(matches) != 27 {
		return nil, fmt.Errorf("fixture registry: got %d languages, expected 27", len(matches))
	}
	languages := []map[string]string{}
	for _, match := range matches {
		languages = append(languages, map[string]string{"code": string(match[1]), "name": string(match[2]), "dir": string(match[3])})
	}
	registry, _ := json.Marshal(languages)
	english := assets["/locales/en.json"]
	if !json.Valid(english) {
		return nil, errors.New("invalid fixture English catalog")
	}
	assets["/locales/en.js"] = []byte("var I18N={en:" + string(english) + "};var UI_LANGUAGES=" + string(registry) + ";")
	return assets, nil
}

func scriptResponse(w http.ResponseWriter, r *http.Request) bool {
	name, ok := strings.CutPrefix(r.URL.Path, "/__ui_checks/")
	if !ok || strings.Contains(name, "/") {
		return false
	}
	data, err := scripts.ReadFile("js/" + name)
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
	return true
}

func writeAsset(w http.ResponseWriter, r *http.Request, assets map[string][]byte) {
	if r.Method != "GET" || path.Clean(r.URL.Path) != r.URL.Path {
		http.NotFound(w, r)
		return
	}
	data, ok := assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	typeName := mime.TypeByExtension(path.Ext(r.URL.Path))
	if r.URL.Path == "/" {
		typeName = "text/html; charset=utf-8"
	}
	if strings.HasSuffix(r.URL.Path, ".js") {
		typeName = "text/javascript; charset=utf-8"
	}
	w.Header().Set("Content-Type", typeName)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func loopbackServer(handler http.Handler) (*httptest.Server, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	return server, nil
}

func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// Explicit external origins are proxied only in this isolated browser context.
// This replaces network interception: models are mocked before app startup,
// while the optional scaling settings writes still reach the owned test server.
func proxyFor(raw string) *httputil.ReverseProxy {
	target, _ := url.Parse(raw)
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		incomingHost := r.Host
		// Retain same-origin validation at the explicitly selected upstream;
		// unrelated origins must not be rewritten.
		if origin, err := url.Parse(r.Header.Get("Origin")); err == nil && origin.Host == incomingHost {
			r.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
		if referer, err := url.Parse(r.Header.Get("Referer")); err == nil && referer.Host == incomingHost {
			referer.Scheme, referer.Host = target.Scheme, target.Host
			r.Header.Set("Referer", referer.String())
		}
		director(r)
		r.Host = target.Host
		r.Header.Set("Accept-Encoding", "identity")
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
		response.Body.Close()
		if err != nil {
			return err
		}
		if len(data) > 8<<20 {
			return errors.New("external HTML exceeds limit")
		}
		data = injectHTML(data)
		response.Body = io.NopCloser(bytes.NewReader(data))
		response.ContentLength = int64(len(data))
		response.Header.Set("Content-Length", fmt.Sprint(len(data)))
		return nil
	}
	return proxy
}

func externalSurface(raw string, models any) (*httptest.Server, error) {
	proxy := proxyFor(raw)
	return loopbackServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scriptResponse(w, r) {
			return
		}
		if models != nil && r.URL.Path == "/api/models" {
			jsonResponse(w, models)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
}
