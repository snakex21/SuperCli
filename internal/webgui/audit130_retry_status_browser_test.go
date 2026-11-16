package webgui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/system/config"
)

// audit130MockProvider is an in-process OpenAI-compatible endpoint: the
// first `fail` chat requests answer with `status` (and Retry-After: 1),
// later ones stream a short reply. No network, no keys, no paid calls.
type audit130MockProvider struct {
	mu       sync.Mutex
	fail     int
	status   int
	requests int
	other    []string
	release  chan struct{} // when set, the success stream waits on it
}

func (p *audit130MockProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		p.mu.Lock()
		p.other = append(p.other, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)
	p.mu.Lock()
	p.requests++
	n := p.requests
	release := p.release
	p.mu.Unlock()
	if n <= p.fail {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(p.status)
		fmt.Fprintf(w, `{"error":{"message":"audit mock HTTP %d","type":"rate_limit_error"}}`, p.status)
		return
	}
	if release != nil {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	chunk := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk(map[string]any{"id": "audit", "object": "chat.completion.chunk", "model": "audit-model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "AUDIT-REPLY-OK"}}}})
	chunk(map[string]any{"id": "audit", "object": "chat.completion.chunk", "model": "audit-model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13}})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (p *audit130MockProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

// While the provider answers 429/503 the GUI must show a localized retry
// notice, and after the retry succeeds the reply arrives and the run status
// returns to Ready (no stale "retrying"/"working" text).
func TestAudit130GUIRetryStatusWhileProviderThrottles(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			mock := &audit130MockProvider{fail: 2, status: status, release: make(chan struct{})}
			provider := httptest.NewServer(mock)
			defer provider.Close()
			app := audit130StartAppWith(t, 1280, 900, func(c *config.Config) {
				c.Provider, c.BaseURL, c.Model, c.APIKey, c.Stream = config.ProviderOpenAI, provider.URL+"/v1", "audit-model", "", true
			})
			app.setLanguage("pl")
			var sent bool
			app.eval(`(async()=>{const p=document.querySelector('#prompt');p.value='audit retry probe';p.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#send-btn').click();return true;})()`, &sent)
			// Both failures are retried with Retry-After: 1 before the stream.
			var during struct {
				Notices []string `json:"notices"`
				Status  string   `json:"status"`
				State   string   `json:"state"`
			}
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				app.eval(audit130RetryState, &during)
				if mock.count() >= 3 && len(during.Notices) >= 2 {
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
			app.shot(fmt.Sprintf("retry_%d_during", status))
			t.Logf("during retries: requests=%d %+v", mock.count(), during)
			if len(during.Notices) < 2 {
				t.Fatalf("expected two retry notices while the provider returned %d, got %q (status %q)", status, during.Notices, during.Status)
			}
			want := "ponawianie za"
			for _, n := range during.Notices {
				if !strings.Contains(n, want) || !strings.Contains(n, fmt.Sprint(status)) {
					t.Errorf("retry notice not localized or lacks the HTTP status: %q", n)
				}
			}
			if !strings.Contains(during.Notices[len(during.Notices)-1], "2/") {
				t.Errorf("second retry notice does not count the attempt: %q", during.Notices[len(during.Notices)-1])
			}
			if during.State != "running" {
				t.Errorf("run state during retries = %q (status %q), want running", during.State, during.Status)
			}
			close(mock.release)
			var after struct {
				Reply  bool   `json:"reply"`
				Status string `json:"status"`
				State  string `json:"state"`
				Key    string `json:"key"`
			}
			deadline = time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				app.eval(`(()=>{const s=document.querySelector('#run-status');return {reply:document.querySelector('#chat-stream, .stream, main').textContent.includes('AUDIT-REPLY-OK'),status:s.textContent,state:s.dataset.state||'',key:s.dataset.runKey||''};})()`, &after)
				if after.Reply && after.State != "running" && after.State != "pending" {
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
			app.shot(fmt.Sprintf("retry_%d_after", status))
			t.Logf("after success: %+v other requests: %v", after, mock.other)
			if !after.Reply {
				t.Fatal("reply never arrived after the retries")
			}
			if after.State == "running" || after.State == "pending" || strings.Contains(after.Status, "ponawian") {
				t.Fatalf("run status not cleared after the successful retry: %+v", after)
			}
		})
	}
}

const audit130RetryState = `(()=>{const s=document.querySelector('#run-status');
 const notices=[...document.querySelectorAll('.event-line')].map(e=>e.textContent).filter(t=>/HTTP (429|503)/.test(t));
 return {notices,status:s.textContent,state:s.dataset.state||''};})()`
