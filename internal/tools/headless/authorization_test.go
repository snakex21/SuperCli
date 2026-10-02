package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/system/config"
	"supercli/internal/tools/interactive"
)

// Existing protocol fixtures opt into their one synthetic endpoint explicitly.
// This helper is test-only; production has no injectable approval bypass.
func fixtureApproval(t *testing.T, ctx context.Context, tool *Tool, protocol, endpoint string) context.Context {
	t.Helper()
	tool.scope = config.HeadlessConf{Targets: map[string]config.HeadlessTargetConf{"fixture": {Protocol: protocol, Endpoint: endpoint, AllowedActions: []string{"open", "status", "inspect", "navigate", "keys", "click", "type", "screenshot", "wait_event", "close"}}}}
	tool.scopeErr = nil
	ch := make(chan interactive.AskRequest)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() {
		for {
			select {
			case req := <-ch:
				req.Respond <- interactive.AskAnswer{Selected: []string{"Allow once"}}
			case <-ctx.Done():
				return
			}
		}
	}()
	return interactive.WithAskChannel(ctx, ch)
}

func writeTarget(t *testing.T, data, protocol, endpoint, actions string) {
	t.Helper()
	text := fmt.Sprintf("[headless.targets.fixture]\nprotocol = %q\nendpoint = %q\nallowed_actions = %s\n", protocol, endpoint, actions)
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestHeadlessScopeDeniesBeforeNetworkOrProfile(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); webdriverReply(w, map[string]any{}) }))
	defer server.Close()
	for _, tc := range []struct{ name, protocol, endpoint, actions string }{
		{"missing", "", "", ""},
		{"empty-actions", "webdriver", server.URL, `[]`},
		{"other-endpoint", "webdriver", server.URL + "/other", `["open"]`},
		{"wrong-protocol", "qmp", strings.Replace(server.URL, "http:", "tcp:", 1), `["open"]`},
		{"read-only", "webdriver", server.URL, `["status"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			if tc.protocol != "" {
				writeTarget(t, data, tc.protocol, tc.endpoint, tc.actions)
			}
			tool := New(t.TempDir(), data)
			raw, _ := json.Marshal(map[string]any{"protocol": "webdriver", "endpoint": server.URL, "action": "open"})
			result, _ := tool.Execute(context.Background(), raw)
			if result.Err == nil {
				t.Fatal("unscoped execution accepted")
			}
			if _, err := os.Stat(filepath.Join(data, ".supercli", "headless")); !os.IsNotExist(err) {
				t.Fatalf("profile created before authorization: %v", err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("denial contacted endpoint")
	}
}
func TestHeadlessMutationNeedsUIAndExactConsent(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); webdriverReply(w, nil) }))
	defer server.Close()
	data := t.TempDir()
	writeTarget(t, data, "webdriver", server.URL, `["navigate"]`)
	tool := New(t.TempDir(), data)
	raw, _ := json.Marshal(map[string]any{"protocol": "webdriver", "endpoint": server.URL, "action": "navigate", "session_id": "known-session", "url": "https://example.com/path"})
	result, _ := tool.Execute(context.Background(), raw)
	if result.Err == nil || requests.Load() != 0 {
		t.Fatal("missing UI did not fail closed")
	}
	for _, allow := range []bool{false, true} {
		ch := make(chan interactive.AskRequest, 1)
		ctx := interactive.WithAskChannel(context.Background(), ch)
		done := make(chan error, 1)
		go func() { r, _ := tool.Execute(ctx, raw); done <- r.Err }()
		req := <-ch
		for _, want := range []string{server.URL, "known-session", "navigate", "https://example.com/path"} {
			if !strings.Contains(req.Question, want) {
				t.Fatalf("missing %q", want)
			}
		}
		if !req.Confirmation || req.Options[0].Label != "Cancel" || requests.Load() != 0 {
			t.Fatal("unsafe consent UI or early dial")
		}
		if allow {
			req.Respond <- interactive.AskAnswer{Selected: []string{"Allow once"}}
		} else {
			req.Respond <- interactive.AskAnswer{Cancelled: true}
		}
		err := <-done
		if allow && err != nil {
			t.Fatal(err)
		}
		if !allow && (err == nil || requests.Load() != 0) {
			t.Fatal("cancelled action ran")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
}
func TestHeadlessReadonlyScopeAndSnapshot(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		webdriverReply(w, map[string]any{"ready": true})
	}))
	defer server.Close()
	data := t.TempDir()
	writeTarget(t, data, "webdriver", server.URL+"/", `["status"]`)
	tool := New(t.TempDir(), data)
	// Later project/global edits cannot widen this tool's captured scope.
	writeTarget(t, data, "webdriver", server.URL, `["open","status"]`)
	raw, _ := json.Marshal(map[string]any{"protocol": "webdriver", "endpoint": server.URL, "action": "status"})
	r, _ := tool.Execute(context.Background(), raw)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	raw, _ = json.Marshal(map[string]any{"protocol": "webdriver", "endpoint": server.URL, "action": "open"})
	r, _ = tool.Execute(context.Background(), raw)
	if r.Err == nil {
		t.Fatal("scope widened after snapshot")
	}
	if requests.Load() != 1 {
		t.Fatal("unexpected requests")
	}
}
func TestHeadlessUnknownActionAndNoImplicitWildcard(t *testing.T) {
	data := t.TempDir()
	writeTarget(t, data, "qmp", "tcp://127.0.0.1:4444", `["*","unknown"]`)
	tool := New(t.TempDir(), data)
	for _, action := range []string{"status", "unknown"} {
		r, _ := tool.Execute(context.Background(), []byte(fmt.Sprintf(`{"protocol":"qmp","endpoint":"tcp://127.0.0.1:4444","action":%q}`, action)))
		if r.Err == nil || strings.Contains(r.Err.Error(), "connect QMP") {
			t.Fatalf("unexpected dial: %v", r.Err)
		}
	}
}
