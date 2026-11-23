package webgui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderIconKeyFromTypeAndAddress(t *testing.T) {
	cases := []struct{ typ, url, want string }{
		{"anthropic", "https://api.anthropic.com/v1", "anthropic"},
		{"claude", "https://api.anthropic.com/v1", "claude"},
		{"codex", "https://chatgpt.com/backend-api/codex", "codex"},
		{"openai", "https://api.openai.com/v1", "openai"},
		{"openai", "http://localhost:1234/v1", "lmstudio"},
		{"openai", "http://127.0.0.1:11434/v1", "ollama"},
		{"openai", "https://opencode.ai/zen/v1", "opencode"},
		{"openai", "https://opencode.ai/inference/openai/v1", "opencode"},
		{"openai", "https://api.kilo.ai/api/openrouter", "kilo"},
		{"openai", "https://openrouter.ai/api/v1", "openrouter"},
		{"openai", "https://generativelanguage.googleapis.com/v1beta/openai", "google"},
		{"openai", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "dashscope"},
		{"openai", "https://token-plan-sgp.xiaomimimo.com/v1", "xiaomi"},
		{"openai", "https://api.x.ai/v1", "xai"},
		{"openai", "https://ark.cn-beijing.volces.com/api/v3", "doubao"},
		// The type alone never picks a brand: gateways get a letter.
		{"anthropic", "https://anyrouter.top/v1", ""},
		{"anthropic", "https://llm.onerouter.pro/v1", ""},
		{"openai", "https://api.orionetics.com/v1", ""},
		{"opencode", "https://gateway.example/v1", ""},
		{"claude", "https://proxy.example/v1", ""},
		{"codex", "https://proxy.example/codex", ""},
		{"openai", "https://logfare.ai/v1", ""},
		{"openai", "http://192.168.0.150:8094/v1", ""},
		{"openai", "https://http--nine-router--8pmb4wzm7yyc.code.run/v1", ""},
		// A look-alike host is not the provider.
		{"openai", "https://api.openai.com.evil.example/v1", ""},
		{"openai", "https://notx.ai/v1", ""},
	}
	for _, c := range cases {
		if got := ProviderIconKey(c.typ, c.url); got != c.want {
			t.Errorf("ProviderIconKey(%q, %q) = %q, want %q", c.typ, c.url, got, c.want)
		}
	}
	// Every key the matcher can return has a bundled logo.
	for _, c := range cases {
		if c.want != "" && !isBuiltinProviderIcon(c.want) {
			t.Errorf("no bundled logo for %q", c.want)
		}
	}
}

func TestBuiltinProviderIconsAreCleanAndClassified(t *testing.T) {
	icons := builtinProviderIcons()
	if len(icons) < 40 {
		t.Fatalf("only %d built-in logos", len(icons))
	}
	mono := map[string]bool{}
	for _, icon := range icons {
		mono[icon.Key] = icon.Mono
		data, err := assetsFS.ReadFile("assets/icons/providers/" + icon.Key + ".svg")
		if err != nil {
			t.Fatal(err)
		}
		clean, err := sanitizeSVG(data)
		if err != nil {
			t.Errorf("%s: %v", icon.Key, err)
			continue
		}
		for _, bad := range []string{"<script", "foreignObject", "onload", "javascript:"} {
			if strings.Contains(string(clean), bad) || strings.Contains(string(data), bad) {
				t.Errorf("%s contains %s", icon.Key, bad)
			}
		}
	}
	if !mono["openai"] || mono["deepseek"] {
		t.Fatalf("mono/colour classification wrong: openai=%v deepseek=%v", mono["openai"], mono["deepseek"])
	}
}

func TestSanitizeSVGRemovesActiveContent(t *testing.T) {
	dirty := `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY x "y">]>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10" onload="alert(1)">
<script>alert(2)</script>
<foreignObject><iframe src="https://evil.example"></iframe></foreignObject>
<a href="javascript:alert(3)"><circle r="4" onclick="alert(4)" fill="red"/></a>
<use xlink:href="https://evil.example/s.svg#a"/><use href="#ok"/>
<image href="https://evil.example/t.png"/>
<rect style="fill:url(https://evil.example/x)" width="1" height="1"/>
<rect style="fill:url(#grad)" width="2" height="2"/>
<animate attributeName="href" to="javascript:alert(5)"/>
<path d="M0 0h10v10z" fill="#123"/></svg>`
	clean, err := sanitizeSVG([]byte(dirty))
	if err != nil {
		t.Fatal(err)
	}
	out := string(clean)
	for _, bad := range []string{"script", "alert", "foreignObject", "iframe", "onload", "onclick", "evil.example", "javascript", "animate", "DOCTYPE", "ENTITY"} {
		if strings.Contains(out, bad) {
			t.Errorf("sanitized SVG still contains %q:\n%s", bad, out)
		}
	}
	for _, keep := range []string{"<path", `d="M0 0h10v10z"`, `href="#ok"`, "url(#grad)", "<circle"} {
		if !strings.Contains(out, keep) {
			t.Errorf("sanitized SVG lost %q:\n%s", keep, out)
		}
	}
	if _, err := sanitizeSVG([]byte(`<html><body>x</body></html>`)); err == nil {
		t.Fatal("a non-SVG document was accepted")
	}
}

func TestProviderLogoImageChecksContentAndSize(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if _, ext, err := providerLogoImage(png); err != nil || ext != ".png" {
		t.Fatalf("png: %q %v", ext, err)
	}
	if _, _, err := providerLogoImage([]byte("MZ\x90\x00 not an image")); err == nil {
		t.Fatal("an executable was accepted as a logo")
	}
	if _, _, err := providerLogoImage(make([]byte, maxProviderLogoBytes+1)); err != errProviderLogoTooBig {
		t.Fatalf("oversized logo: %v", err)
	}
}

// Logo choice, upload, rename, edit and delete through the HTTP API, with a
// provider whose name says nothing about its address.
func TestProviderLogoLifecycleAndEditKeepsFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer upstream.Close()
	srv := newTestServer(t, false)
	call := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localProviderJSONRequest(method, target, body))
		return rec
	}
	if rec := call(http.MethodPost, "/api/providers", `{"name":"ja","type":"openai","base_url":"`+upstream.URL+`/v1","api_key":"fixture-key","model":"m1"}`); rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	list := func() providerRow {
		rec := call(http.MethodGet, "/api/providers", "")
		var got struct{ Providers []providerRow }
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Providers) != 1 {
			t.Fatalf("list: %v %s", err, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "fixture-key") {
			t.Fatal("the list exposed the API key")
		}
		return got.Providers[0]
	}
	if p := list(); p.Icon != "" || p.Logo != nil {
		t.Fatalf("a local gateway got a logo from nowhere: %+v", p)
	}
	// Built-in choice.
	if rec := call(http.MethodPost, "/api/provider/logo", `{"name":"ja","builtin":"anthropic"}`); rec.Code != http.StatusOK {
		t.Fatalf("builtin: %d %s", rec.Code, rec.Body.String())
	}
	if p := list(); p.Logo["builtin"] != "anthropic" || p.Icon != "" {
		t.Fatalf("custom logo must win over (no) matched icon: %+v", p)
	}
	if rec := call(http.MethodPost, "/api/provider/logo", `{"name":"ja","builtin":"../../etc"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown builtin accepted: %d", rec.Code)
	}
	// Upload (SVG, sanitized) and serve it.
	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="x()"><script>x()</script><circle r="3"/></svg>`))
	if rec := call(http.MethodPost, "/api/provider/logo", `{"name":"ja","filename":"me.svg","data":"`+svg+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	p := list()
	if !strings.HasPrefix(p.Logo["url"], "/api/provider/logo?name=ja&v=") {
		t.Fatalf("uploaded logo url: %+v", p.Logo)
	}
	served := call(http.MethodGet, "/api/provider/logo?name=ja", "")
	if served.Code != http.StatusOK || served.Header().Get("Content-Type") != "image/svg+xml" || strings.Contains(served.Body.String(), "script") || strings.Contains(served.Body.String(), "onload") {
		t.Fatalf("served logo: %d %q %s", served.Code, served.Header().Get("Content-Type"), served.Body.String())
	}
	bad := base64.StdEncoding.EncodeToString([]byte("MZ not an image"))
	if rec := call(http.MethodPost, "/api/provider/logo", `{"name":"ja","data":"`+bad+`"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "prov.logoInvalid") {
		t.Fatalf("invalid upload: %d %s", rec.Code, rec.Body.String())
	}
	// Editing the address keeps the key, model and logo.
	if rec := call(http.MethodPut, "/api/providers", `{"name":"ja","base_url":"`+upstream.URL+`/v1/"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	m := srv.eng.providerManager()
	m.Reload()
	conf := m.Configured()
	if len(conf) != 1 || conf[0].APIKey != "fixture-key" || conf[0].Model != "m1" || conf[0].Type != "openai" {
		t.Fatalf("edit lost fields: %+v", conf)
	}
	// Rename carries the logo; delete removes it and its file.
	if rec := call(http.MethodPost, "/api/provider/rename", `{"name":"ja","new_name":"moj"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	logos := loadProviderLogos(srv.eng.DataDir())
	file := logos["moj"].File
	if file == "" || logos["ja"].File != "" {
		t.Fatalf("rename did not move the logo: %+v", logos)
	}
	usage := call(http.MethodGet, "/api/provider/usage?name=moj", "")
	if usage.Code != http.StatusOK || !strings.Contains(usage.Body.String(), `"active"`) {
		t.Fatalf("usage: %d %s", usage.Code, usage.Body.String())
	}
	if rec := call(http.MethodDelete, "/api/providers?name=moj", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := loadProviderLogos(srv.eng.DataDir())["moj"]; ok {
		t.Fatal("delete kept the logo choice")
	}
	if _, err := os.Stat(filepath.Join(providerLogoDir(srv.eng.DataDir()), file)); !os.IsNotExist(err) {
		t.Fatalf("delete kept the logo file: %v", err)
	}
	m.Reload()
	if len(m.Configured()) != 0 {
		t.Fatalf("provider still configured: %+v", m.Configured())
	}
}
