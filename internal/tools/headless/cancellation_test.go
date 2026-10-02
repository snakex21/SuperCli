package headless

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"supercli/internal/tools/core"
	"testing"
)

func TestExecutePreservesCreatedSessionWhenNavigationCanceled(t *testing.T) {
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/session" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"value":{"sessionId":"owned-cancel-fixture","capabilities":{}}}`)
			return
		}
		if r.URL.Path == "/session/owned-cancel-fixture/url" {
			close(ready)
			<-r.Context().Done()
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tool := New(t.TempDir(), t.TempDir())
	ctx = fixtureApproval(t, ctx, tool, "webdriver", server.URL)
	resultCh := make(chan core.Result, 1)
	raw := []byte(`{"protocol":"webdriver","endpoint":"` + server.URL + `","action":"open","url":"https://example.com"}`)
	go func() { result, _ := tool.Execute(ctx, raw); resultCh <- result }()
	<-ready
	cancel()
	result := <-resultCh
	if !errors.Is(result.Err, context.Canceled) || !strings.Contains(result.Err.Error(), "owned-cancel-fixture") {
		t.Fatal(result.Err)
	}
}
