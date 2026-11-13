package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func call(t *testing.T, tool *Tool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Go error: %v", err)
	}
	return result.Text, result.Err
}

func TestBrowserValidatesBeforeStartingABrowser(t *testing.T) {
	tool := New()
	tool.start = func(context.Context) (*session, error) {
		t.Fatal("browser started for an invalid request")
		return nil, nil
	}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "open", "url": "http://127.0.0.1:9/"}, "private or local"},
		{map[string]any{"action": "open", "url": "file:///etc/passwd"}, "http and https"},
		{map[string]any{"action": "click", "ref": "e1"}, "no page is open"},
		{map[string]any{"action": "click", "ref": "body > a"}, "ref must look like"},
		{map[string]any{"action": "hover"}, "action must be"},
	} {
		_, err := call(t, tool, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err=%v, want %q", tc.args, err, tc.want)
		}
	}
	if text, err := call(t, tool, map[string]any{"action": "close"}); err != nil || !strings.Contains(text, "No browser") {
		t.Fatalf("close without browser: %q %v", text, err)
	}
}

func TestBrowserReportsMissingBrowserClearly(t *testing.T) {
	tool := New()
	tool.checkURL = func(context.Context, string) error { return nil }
	tool.start = func(context.Context) (*session, error) {
		return nil, errors.New("could not start a headless Chrome/Edge (install one)")
	}
	_, err := call(t, tool, map[string]any{"action": "open", "url": "https://example.org/"})
	if err == nil || !strings.Contains(err.Error(), "headless Chrome/Edge") {
		t.Fatalf("missing browser not explained: %v", err)
	}
}

var refPattern = regexp.MustCompile(`\[(e\d+)\] (\w+) "([^"]*)"`)

func refFor(t *testing.T, text, role, name string) string {
	t.Helper()
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		if m[2] == role && strings.Contains(m[3], name) {
			return m[1]
		}
	}
	t.Fatalf("no %s %q in:\n%s", role, name, text)
	return ""
}

// TestBrowserOpenClickTypeBack drives a real headless Chrome/Edge. It is
// skipped where none is installed. Run browser packages with -p 1.
func TestBrowserOpenClickTypeBack(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Start</title></head><body><main><h1>Start page</h1>
<p>Static introduction text.</p><div id="app"></div>
<a href="/second">Next page</a>
<form action="/search"><input name="q" placeholder="Search"><button>Go</button></form>
<script>document.getElementById("app").textContent = "Rendered by script: 42 items";</script></main></body></html>`)
	})
	mux.HandleFunc("/second", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Second</title></head><body><p>First quote on page two is by Marilyn Monroe.</p></body></html>`)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><head><title>Results</title></head><body><p>Results for %s</p></body></html>`, r.URL.Query().Get("q"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tool := New()
	tool.checkURL = func(context.Context, string) error { return nil } // loopback fixture
	defer tool.Close()
	text, err := call(t, tool, map[string]any{"action": "open", "url": server.URL + "/"})
	if err != nil {
		if strings.Contains(err.Error(), "no dev-only test browser") {
			t.Skip("no Chrome/Edge installed")
		}
		t.Fatal(err)
	}
	if !strings.Contains(text, "Rendered by script: 42 items") || !strings.Contains(text, "<<<page-elements ") {
		t.Fatalf("rendered DOM or fenced elements missing:\n%s", text)
	}
	link := refFor(t, text, "link", "Next page")
	box := refFor(t, text, "textbox", "Search")

	text, err = call(t, tool, map[string]any{"action": "click", "ref": link})
	if err != nil || !strings.Contains(text, "Marilyn Monroe") || !strings.Contains(text, "Navigated: ") {
		t.Fatalf("click did not follow the link: %v\n%s", err, text)
	}
	text, err = call(t, tool, map[string]any{"action": "back"})
	if err != nil || !strings.Contains(text, "Start page") {
		t.Fatalf("back failed: %v\n%s", err, text)
	}
	// The ref of a re-rendered document is assigned again in the same order.
	box = refFor(t, text, "textbox", "Search")
	text, err = call(t, tool, map[string]any{"action": "type", "ref": box, "text": "zażółć", "submit": true})
	if err != nil || !strings.Contains(text, "Results for zażółć") {
		t.Fatalf("type+submit failed: %v\n%s", err, text)
	}
	if _, err := call(t, tool, map[string]any{"action": "click", "ref": "e999"}); err == nil || !strings.Contains(err.Error(), "no element with that ref") {
		t.Fatalf("stale ref not explained: %v", err)
	}
	text, err = call(t, tool, map[string]any{"action": "close"})
	if err != nil || text != "Browser closed." || tool.sess != nil {
		t.Fatalf("close failed: %q %v", text, err)
	}
}
