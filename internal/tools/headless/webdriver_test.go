package headless

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

func webdriverReply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
}

func webdriverPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 20))
	img.Set(1, 1, color.NRGBA{R: 200, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func webdriverResult(t *testing.T, text string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWebdriverOpenOwnsPortableHeadlessProfileAndExplicitClose(t *testing.T) {
	for _, browser := range []string{"chrome", "firefox"} {
		t.Run(browser, func(t *testing.T) {
			app := t.TempDir()
			outside := t.TempDir()
			marker := filepath.Join(outside, "keep.txt")
			if err := os.WriteFile(marker, []byte("external"), 0600); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(app, "test-browser")
			if err := os.WriteFile(binary, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			var profile string
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "POST /session":
					var request struct {
						Capabilities struct {
							AlwaysMatch map[string]any `json:"alwaysMatch"`
						} `json:"capabilities"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					caps := request.Capabilities.AlwaysMatch
					if caps["browserName"] != browser {
						t.Errorf("browser capability: %v", caps)
					}
					var key string
					if browser == "chrome" {
						key = "goog:chromeOptions"
					} else {
						key = "moz:firefoxOptions"
					}
					options := caps[key].(map[string]any)
					args := options["args"].([]any)
					if options["binary"] != binary {
						t.Errorf("binary: %v", options["binary"])
					}
					if browser == "chrome" {
						if args[0] != "--headless=new" {
							t.Errorf("headless args: %v", args)
						}
						for _, arg := range args {
							if strings.HasPrefix(arg.(string), "--user-data-dir=") {
								profile = strings.TrimPrefix(arg.(string), "--user-data-dir=")
							}
						}
					} else {
						if !reflect.DeepEqual(args[:2], []any{"-headless", "-profile"}) {
							t.Errorf("headless args: %v", args)
						}
						profile = args[2].(string)
					}
					portableRoot := filepath.Join(app, ".supercli", "headless")
					// Windows temp paths may use RUNNER~1 while the sandbox returns
					// the long canonical path. Assert directory identity, not spelling.
					expectedParent, expectedErr := os.Stat(portableRoot)
					actualParent, actualErr := os.Stat(filepath.Dir(profile))
					if expectedErr != nil || actualErr != nil || !os.SameFile(expectedParent, actualParent) {
						t.Errorf("profile outside app: %s (expected parent %s; stat errors %v, %v)", profile, portableRoot, expectedErr, actualErr)
					}
					if _, err := os.Stat(profile); err != nil {
						t.Errorf("profile before session creation: %v", err)
					}
					timeouts := caps["timeouts"].(map[string]any)
					if timeouts["implicit"] != float64(0) {
						t.Errorf("implicit polling enabled: %v", timeouts)
					}
					webdriverReply(w, map[string]any{"sessionId": "fixture-1", "capabilities": map[string]any{"browserName": browser}})
				case "POST /session/fixture-1/url":
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["url"] != "https://example.test/start" {
						t.Errorf("navigation: %v", body)
					}
					webdriverReply(w, nil)
				case "DELETE /session/fixture-1", "DELETE /session/external-2":
					webdriverReply(w, nil)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			tool := &Tool{BaseDir: app, DataDir: app}
			result, err := tool.runWebDriver(context.Background(), params{Action: "open", Endpoint: server.URL, Browser: browser, Binary: binary, URL: "https://example.test/start"})
			if err != nil {
				t.Fatal(err)
			}
			got := webdriverResult(t, result.Text)
			if got["session_id"] != "fixture-1" || got["headless"] != true || got["owned"] != true {
				t.Fatalf("open result: %s", result.Text)
			}
			record := webdriverOwnershipPath(filepath.Join(app, ".supercli", "headless"), server.URL, "fixture-1")
			if _, err := os.Stat(record); err != nil {
				t.Fatal(err)
			}
			if _, err := tool.runWebDriver(context.Background(), params{Action: "close", Endpoint: server.URL, SessionID: "external-2"}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(profile); err != nil {
				t.Fatalf("closing attached session deleted owned profile: %v", err)
			}
			if _, err := tool.runWebDriver(context.Background(), params{Action: "close", Endpoint: server.URL, SessionID: "fixture-1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(profile); !os.IsNotExist(err) {
				t.Fatalf("closed profile retained: %v", err)
			}
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("closed ownership retained: %v", err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("external data removed: %v", err)
			}
			want := []string{"POST /session", "POST /session/fixture-1/url", "DELETE /session/external-2", "DELETE /session/fixture-1"}
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("extra requests: %v", requests)
			}
		})
	}
}

func TestWebdriverActionsUseNativeProtocolAndMetadataBeforePixels(t *testing.T) {
	data := webdriverPNG(t)
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /wd/hub/status":
			webdriverReply(w, map[string]any{"ready": true})
		case "GET /wd/hub/session/session-1/title":
			webdriverReply(w, "Fixture")
		case "GET /wd/hub/session/session-1/url":
			webdriverReply(w, "https://example.test/")
		case "POST /wd/hub/session/session-1/execute/sync":
			var body struct {
				Script string `json:"script"`
				Args   []any  `json:"args"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Script != webdriverInspectScript || len(body.Args) != 0 {
				t.Errorf("inspect accepted dynamic script: %v", body)
			}
			webdriverReply(w, map[string]any{"title": "Fixture", "text": "Hello", "elements": []any{map[string]any{"selector": "#submit", "tag": "button", "label": "Send"}}, "truncated": false})
		case "POST /wd/hub/session/session-1/url":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["url"] != "https://example.test/next" {
				t.Errorf("navigate: %v", body)
			}
			webdriverReply(w, nil)
		case "POST /wd/hub/session/session-1/element":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["using"] != "css selector" || body["value"] != "#submit" {
				t.Errorf("selector: %v", body)
			}
			webdriverReply(w, map[string]string{webdriverElementKey: "element-1"})
		case "POST /wd/hub/session/session-1/element/element-1/click":
			webdriverReply(w, nil)
		case "POST /wd/hub/session/session-1/element/element-1/value":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["text"] != "Zażółć 😀" {
				t.Errorf("text: %v", body)
			}
			webdriverReply(w, nil)
		case "GET /wd/hub/session/session-1/screenshot":
			webdriverReply(w, base64.StdEncoding.EncodeToString(data))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	tool := &Tool{DataDir: t.TempDir()}
	endpoint := server.URL + "/wd/hub"
	for _, p := range []params{
		{Action: "status", Endpoint: endpoint},
		{Action: "status", Endpoint: endpoint, SessionID: "session-1"},
		{Action: "inspect", Endpoint: endpoint, SessionID: "session-1"},
		{Action: "navigate", Endpoint: endpoint, SessionID: "session-1", URL: "https://example.test/next"},
		{Action: "click", Endpoint: endpoint, SessionID: "session-1", Selector: "#submit"},
		{Action: "type", Endpoint: endpoint, SessionID: "session-1", Selector: "#submit", Text: "Zażółć 😀"},
		{Action: "screenshot", Endpoint: endpoint, SessionID: "session-1"},
	} {
		result, err := tool.runWebDriver(context.Background(), p)
		if err != nil {
			t.Fatalf("%s: %v", p.Action, err)
		}
		if result.Image != nil {
			t.Fatalf("%s unexpectedly attaches pixels", p.Action)
		}
		if p.Action == "inspect" && !strings.Contains(result.Text, "#submit") {
			t.Errorf("semantic inspector lost target: %s", result.Text)
		}
	}
	if len(requests) != 10 {
		t.Fatalf("unexpected HTTP round trips: %v", requests)
	}
	result, err := tool.runWebDriver(context.Background(), params{Action: "screenshot", Endpoint: endpoint, SessionID: "session-1", Attach: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Image == nil || !bytes.Equal(result.Image.Data, data) {
		t.Fatal("explicit visual analysis missing")
	}
}

func TestWebdriverRejectsInvalidInputBeforeContactOrProfileCreation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); webdriverReply(w, nil) }))
	defer server.Close()
	app := t.TempDir()
	tool := &Tool{DataDir: app, BaseDir: app}
	tests := []params{
		{Action: "open", Browser: "opera"},
		{Action: "open", SessionID: "existing"},
		{Action: "open", URL: "javascript:alert(1)"},
		{Action: "open", URL: "https://user:secret@example.test/"},
		{Action: "open", Binary: "missing-browser"},
		{Action: "click", SessionID: "valid", Selector: ""},
		{Action: "type", SessionID: "valid", Selector: "#input", Text: strings.Repeat("x", 16<<10+1)},
		{Action: "type", SessionID: "valid", Selector: "#input", Text: string([]byte{0xff})},
		{Action: "status", SessionID: "../../else"},
		{Action: "status", SessionID: "%2Ffoo"},
		{Action: "status", SessionID: "."},
		{Action: "close", SessionID: ""},
		{Action: "screenshot", SessionID: "valid", ImageDetail: "low"},
		{Action: "navigate", SessionID: "valid", URL: "file:///outside"},
		{Action: "launch"},
	}
	for _, p := range tests {
		p.Endpoint = server.URL
		if _, err := tool.runWebDriver(context.Background(), p); err == nil {
			t.Errorf("accepted invalid params: %+v", p)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid input contacted driver %d times", requests.Load())
	}
	if _, err := os.Stat(filepath.Join(app, ".supercli")); !os.IsNotExist(err) {
		t.Fatalf("invalid input created app data: %v", err)
	}
	for _, endpoint := range []string{"http://localhost:4444", "http://192.0.2.1:4444", "http://127.0.0.1:4444?x=1", "http://user@127.0.0.1:4444", "https://127.0.0.1:4444"} {
		if _, err := newWebdriverClient(endpoint); err == nil {
			t.Errorf("unsafe endpoint accepted: %s", endpoint)
		}
	}
}

func TestWebdriverErrorsLimitsCancellationAndNoRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); webdriverReply(w, nil) }))
	defer target.Close()
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"protocol", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			webdriverReply(w, map[string]any{"error": "invalid session id", "message": "session missing", "stacktrace": strings.Repeat("private", 100)})
		}, "invalid session id"},
		{"error-on-200", func(w http.ResponseWriter, r *http.Request) {
			webdriverReply(w, map[string]any{"error": "stale element reference", "message": "target changed"})
		}, "stale element reference"},
		{"malformed", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "not json") }, "invalid JSON"},
		{"missing-value", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "{}") }, "no value"},
		{"oversized", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", webdriverSmallLimit+1))
		}, "exceeds"},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }, "redirect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			_, err := (&Tool{}).runWebDriver(context.Background(), params{Action: "status", Endpoint: server.URL})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error: %v", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("driver stacktrace exposed")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
	t.Run("canceled", func(t *testing.T) {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := (&Tool{}).runWebDriver(ctx, params{Action: "status", Endpoint: server.URL})
			done <- err
		}()
		<-started
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("canceled operation succeeded")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancellation did not abort HTTP")
		}
	})
}

func TestWebdriverScreenshotValidatesBytes(t *testing.T) {
	for _, value := range []any{"%%%", "dGV4dA==", map[string]string{"wrong": "shape"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webdriverReply(w, value) }))
		_, err := (&Tool{DataDir: t.TempDir()}).runWebDriver(context.Background(), params{Action: "screenshot", Endpoint: server.URL, SessionID: "fixture"})
		server.Close()
		if err == nil {
			t.Errorf("invalid screenshot accepted: %v", value)
		}
	}
}

func TestWebdriverFailedOpenRemovesOnlyNewProfile(t *testing.T) {
	app := t.TempDir()
	root := filepath.Join(app, ".supercli", "headless")
	existing := filepath.Join(root, "profile-previous")
	if err := os.MkdirAll(existing, 0700); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		webdriverReply(w, map[string]any{"error": "session not created", "message": "browser unavailable"})
	}))
	defer server.Close()
	if _, err := (&Tool{DataDir: app}).runWebDriver(context.Background(), params{Action: "open", Endpoint: server.URL}); err == nil {
		t.Fatal("failed session creation succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "profile-previous" {
		t.Fatalf("failed-open cleanup: %v", entries)
	}
}

func TestWebdriverOwnershipCleanupRejectsOutsideAndSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "headless")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	recordPath := webdriverOwnershipPath(root, "http://127.0.0.1:4444", "fixture")
	write := func(profile string) {
		t.Helper()
		data, _ := json.Marshal(webdriverOwnership{Endpoint: "http://127.0.0.1:4444", SessionID: "fixture", Profile: profile})
		if err := os.WriteFile(recordPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(outside)
	if err := webdriverCleanupProfile(root, "http://127.0.0.1:4444", "fixture"); err == nil {
		t.Fatal("outside profile cleanup allowed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "profile-link")
	if err := os.Symlink(outside, link); err == nil {
		write(link)
		if err := webdriverCleanupProfile(root, "http://127.0.0.1:4444", "fixture"); err == nil {
			t.Fatal("symlink cleanup allowed")
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal(err)
		}
	}
	write("profile-missing")
	if err := webdriverCleanupProfile(root, "http://127.0.0.1:4444", "other"); err != nil {
		t.Fatalf("unknown session close must not read another record: %v", err)
	}
	if err := webdriverCleanupProfile(root, "http://127.0.0.1:4444", "fixture"); err != nil {
		t.Fatal(err)
	}
}

func TestWebdriverTransportIgnoresProxyEnvironment(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); http.Error(w, "proxy", 500) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webdriverReply(w, map[string]bool{"ready": true}) }))
	defer server.Close()
	client, err := newWebdriverClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if client.transport.Proxy != nil || !client.transport.DisableKeepAlives {
		t.Fatal("non-ephemeral/proxied transport")
	}
	if _, err = client.call(context.Background(), http.MethodGet, "/status", nil, webdriverSmallLimit); err != nil {
		t.Fatal(err)
	}
	if proxyCalls.Load() != 0 {
		t.Fatal("driver request used environment proxy")
	}
}

func TestWebdriverInspectScriptHasBoundedTraversalAndNoDynamicInput(t *testing.T) {
	for _, marker := range []string{"visited++<2000", "result.text.length<12000", "result.elements.length>=60", "if(budget+size>16000)", "n.type!==\"password\""} {
		if !strings.Contains(webdriverInspectScript, marker) {
			t.Fatalf("missing inspector bound %q", marker)
		}
	}
	if strings.Contains(webdriverInspectScript, "arguments[") || strings.Contains(webdriverInspectScript, "eval(") {
		t.Fatal("inspector allows supplied script")
	}
}

func TestWebdriverInspectRealScriptHidesTextAndAdvertisesOnlyUniqueSelectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 800, Height: 600})
	if err != nil {
		t.Fatal(err) // dev-only Chrome absence is an error, never an implicit skip
	}
	defer func() {
		if err := browser.Close(); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><head><title>Fixture</title></head><body></body></html>")
	}))
	defer server.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	fixture := `(function () {
const check=(condition,message)=>{if(!condition)throw Error(message);};
const eq=(actual,expected,message)=>check(actual===expected,message+": "+JSON.stringify({actual,expected}));
const body=document.body;body.style.margin="0";
body.innerHTML='<p>Visible greeting</p>';
function add(tag, parent=body, text="") {const node=document.createElement(tag);node.textContent=text;parent.append(node);return node;}
const secrets=[];
for(const [name,attributes,style] of [
 ["display-none",{},"display:none"], ["hidden-attribute",{hidden:""},""],
 ["aria-hidden",{"aria-hidden":"true"},""], ["visibility-hidden",{},"visibility:hidden"],
 ["visibility-collapse",{},"visibility:collapse"], ["opacity-zero",{},"opacity:0"],
 ["zero-width",{},"width:0;height:20px;overflow:hidden"], ["zero-height",{},"height:0;overflow:hidden"],
 ["below",{},"position:fixed;top:1200px"], ["above",{},"position:fixed;top:-100px"],
 ["left",{},"position:fixed;left:-1200px"], ["right",{},"position:fixed;left:1200px"]
]) {
 const parent=add("div");Object.assign(parent.style,{position:"absolute",width:"120px",height:"20px"});parent.style.cssText+=";"+style;
 for(const [key,value] of Object.entries(attributes))parent.setAttribute(key,value);
 const secret="Secret-"+name;secrets.push(secret);
 if(name.startsWith("zero-")){parent.textContent=secret;const control=add("button",body,secret+"-control");control.style.cssText="position:absolute;width:0;height:0;padding:0;border:0;overflow:hidden";}
 else {add("span",parent,secret);add("button",parent,secret+"-control");}
}
for(const tag of ["script","style","noscript","template"]){
 const node=document.createElement(tag);if(tag==="script")node.type="application/json";
 const secret="Secret-"+tag;secrets.push(secret);node.textContent=secret;body.append(node);
}
for(let i=0;i<2;i++){let parent=add("section");for(let j=0;j<10;j++)parent=add("div",parent);add("button",parent,"Nested "+i);}
for(let i=0;i<2;i++){const node=add("button",body,"Duplicate "+i);node.id="duplicate";}
const escaped=add("button",body,"Escaped ID");escaped.id="special:id.with[syntax]";
const password=add("input");password.type="password";password.value="do-not-return";
add("p",body,"x".repeat(20000));
`
	fixture += "\nfunction inspect(){\n" + webdriverInspectScript + "\n}\n"
	fixture += `
const output=inspect();
check(output.text.includes("Visible greeting"),"visible text missing");
for(const secret of secrets)check(!JSON.stringify(output).includes(secret),"hidden text/control leaked: "+secret);
eq(output.truncated,true,"large text truncation");
check(output.elements.length>=5,JSON.stringify(output));
for(const item of output.elements){
 const matches=document.querySelectorAll(item.selector);eq(matches.length,1,"non-unique "+item.selector);
 check(matches[0].matches("a[href],button,input,select,textarea,[role='button'],[role='link'],[contenteditable='true']"),"non-interactive selector");
}
eq(output.elements.filter(v=>v.label.startsWith("Nested")).length,2,"nested controls");
eq(output.elements.filter(v=>v.label.startsWith("Duplicate")).length,2,"duplicate ID controls");
eq(output.elements.filter(v=>v.selector==="#duplicate").length,0,"ambiguous ID advertised");
check(output.elements.some(v=>v.selector==="#"+CSS.escape(escaped.id)),"native escaped ID missing");
check(output.elements.every(v=>v.value!=="do-not-return"),"password value leaked");
check(JSON.stringify(output).length<=16000,"inspector output exceeds bound");
return {elements:output.elements.length,hiddenCases:secrets.length,truncated:output.truncated,bytes:JSON.stringify(output).length};
})()
`
	var report struct {
		Elements, HiddenCases, Bytes int
		Truncated                    bool
	}
	if err := page.Eval(ctx, fixture, &report); err != nil {
		t.Fatalf("native DOM inspector fixture: %v", err)
	}
	t.Logf("original inspector in native Chrome: %d unique interactive selectors, %d hidden/non-text cases, truncated=%v, %d bytes", report.Elements, report.HiddenCases, report.Truncated, report.Bytes)
}

func TestWebdriverOpenRejectsProfileRootOutsidePortableData(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, ".supercli"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dataDir, ".supercli", "headless")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("must not contact driver after unsafe profile root")
	}))
	defer server.Close()
	tool := New(t.TempDir(), dataDir)
	_, err := tool.runWebDriver(context.Background(), params{Action: "open", Endpoint: server.URL})
	if err == nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside app", err)
	}
}
