package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRequestTraceComparisons(t *testing.T) {
	for _, tc := range []struct{ name, before, after, state string }{
		{"same", "abc", "abc", "identical"},
		{"append partial", "abc", "abcd", "append"},
		{"append boundary", strings.Repeat("a", 1024), strings.Repeat("a", 1024) + "b", "append"},
		{"start", "abc", "xbc", "changed"},
		{"second block", strings.Repeat("a", 2300), strings.Repeat("a", 1300) + "b" + strings.Repeat("a", 999), "changed"},
		{"shorter", strings.Repeat("a", 2100), strings.Repeat("a", 1100), "changed"},
		{"shorter boundary", strings.Repeat("a", 2100), strings.Repeat("a", 1024), "changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newRequestTraceTracker()
			key := tracker.digest([]byte(tc.name))
			first := tracker.compare(key, key, []byte(tc.before), "private-key")
			got := tracker.compare(key, key, []byte(tc.after), "private-key")
			if first.State != "first" || got.State != tc.state || got.PreviousSequence != first.Sequence || got.CacheKeyChanged == nil || *got.CacheKeyChanged {
				t.Fatalf("comparison first=%+v got=%+v", first, got)
			}
			if got.State != "identical" {
				at := 0
				for at < min(len(tc.before), len(tc.after)) && tc.before[at] == tc.after[at] {
					at++
				}
				if got.FirstDiffFrom == nil || got.FirstDiffTo == nil || int64(at) < *got.FirstDiffFrom || int64(at) > *got.FirstDiffTo || *got.FirstDiffTo-*got.FirstDiffFrom >= requestTraceBlock {
					t.Fatalf("actual first diff %d outside %+v", at, got)
				}
			}
		})
	}
}

type requestTraceTransport func(*http.Request) (*http.Response, error)

func (f requestTraceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type requestTraceTestProvider struct {
	client *http.Client
	bodies []string
}

func (*requestTraceTestProvider) Name() string { return "trace-test-model" }
func (p *requestTraceTestProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	for _, body := range p.bodies {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://private-host.invalid/responses", strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer PRIVATE_AUTH")
		req.Header.Set("session_id", "PRIVATE_CACHE")
		req.Header.Set("chatgpt-account-id", "PRIVATE_ACCOUNT")
		resp, err := doWithResponseHeaderTimeout(p.client, req, 0, nil)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
	}
	ch := make(chan Delta, 1)
	ch <- Delta{Content: "ok", Usage: &Usage{Input: 100, Output: 2, HasCachedInput: true}}
	close(ch)
	return ch, nil
}

func requestTraceClient(t *testing.T, expected string) *http.Client {
	t.Helper()
	return &http.Client{Transport: requestTraceTransport(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if expected != "" && string(body) != expected {
			t.Errorf("wire body changed")
		}
		if req.Header.Get("Authorization") != "Bearer PRIVATE_AUTH" {
			t.Errorf("auth header changed")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})}
}

func requestTraceRun(t *testing.T, p Provider, ctx context.Context, stats <-chan CallStat) CallStat {
	t.Helper()
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "not the wire payload"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	return <-stats
}

func TestRequestTraceWirePrivacyScopeAndRetries(t *testing.T) {
	body := `{"model":"PRIVATE_MODEL","input":"PRIVATE_PROMPT and PRIVATE_TOOL_RESULT","prompt_cache_key":"PRIVATE_CACHE"}`
	stats := make(chan CallStat, 20)
	base := &requestTraceTestProvider{client: requestTraceClient(t, body), bodies: []string{body}}
	p := MeteredConnection(base, "test", "https://private-host.invalid", "PRIVATE_AUTH", PurposeMain, func(s CallStat) { stats <- s })
	ctx := WithOpenCodeSession(context.Background(), t.TempDir())
	first := requestTraceRun(t, p, ctx, stats)
	second := requestTraceRun(t, p, ctx, stats)
	if first.RequestTrace.First.State != "first" || second.RequestTrace.First.State != "identical" || second.RequestTrace.First.SessionHeaderMatchesCacheKey == nil || !*second.RequestTrace.First.SessionHeaderMatchesCacheKey || !second.HasCachedInput || second.TokensCached != 0 {
		t.Fatalf("bad measured state: %+v / %+v", first, second)
	}
	encoded, err := json.Marshal(second.RequestTrace)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE", "private-host", "responses", "not the wire payload", "Bearer"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("private text leaked: %s", secret)
		}
	}
	if SafeRequestTrace(second.RequestTrace) == nil {
		t.Fatal("generated trace rejected")
	}
	for _, other := range []context.Context{
		WithOpenCodeSession(ctx, "other-"+openCodeSessionFromContext(ctx)), WithPurpose(ctx, PurposeCompact),
		WithPromptCacheScope(ctx, "worker-a"), WithPromptCacheScope(ctx, "worker-b"),
	} {
		if got := requestTraceRun(t, p, other, stats).RequestTrace.First; got.State != "first" {
			t.Fatalf("scope mixed: %+v", got)
		}
	}
	otherConn := MeteredConnection(base, "test", "https://private-host.invalid", "other-auth", PurposeMain, func(s CallStat) { stats <- s })
	if got := requestTraceRun(t, otherConn, ctx, stats).RequestTrace.First; got.State != "first" {
		t.Fatalf("connection mixed: %+v", got)
	}
	if got := requestTraceRun(t, p, context.Background(), stats).RequestTrace.First; got.Unavailable != "no_session" {
		t.Fatalf("unscoped=%+v", got)
	}
	base.client = requestTraceClient(t, "")
	base.bodies = []string{body, strings.ReplaceAll(body, "PRIVATE_CACHE", "OTHER_CACHE")}
	retry := requestTraceRun(t, p, ctx, stats).RequestTrace
	if retry.Attempts != 2 || retry.First.State != "identical" || retry.Last == nil || retry.Last.CacheKeyChanged == nil || !*retry.Last.CacheKeyChanged || retry.Last.SessionHeaderMatchesCacheKey == nil || *retry.Last.SessionHeaderMatchesCacheKey {
		t.Fatalf("retry lost first/last: %+v", retry)
	}
}

func TestRequestTraceLimitsAndUnavailable(t *testing.T) {
	ctx := WithOpenCodeSession(context.Background(), t.TempDir())
	stats := make(chan CallStat, 3)
	base := &requestTraceTestProvider{client: requestTraceClient(t, ""), bodies: []string{`{"model":"m"}`}}
	p := Metered(base, "limits", PurposeMain, func(s CallStat) { stats <- s })
	requestTraceRun(t, p, ctx, stats)
	base.bodies = []string{`{"model":"m","input":"` + strings.Repeat("a", requestTraceMaxBody) + `"}`}
	if got := requestTraceRun(t, p, ctx, stats).RequestTrace.First; got.Unavailable != "body_limit" {
		t.Fatalf("oversize=%+v", got)
	}
	base.bodies = []string{`{"model":"m"}`}
	if got := requestTraceRun(t, p, ctx, stats).RequestTrace.First; got.State != "first" {
		t.Fatalf("baseline retained after skipped body: %+v", got)
	}
	tracker := newRequestTraceTracker()
	for i := 0; i < requestTraceMaxStreams+5; i++ {
		key := tracker.digest([]byte(fmt.Sprint(i)))
		tracker.compare(key, key, []byte("abc"), "")
	}
	if len(tracker.streams) != requestTraceMaxStreams {
		t.Fatalf("unbounded streams=%d", len(tracker.streams))
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := tracker.digest([]byte(fmt.Sprint(i)))
			tracker.compare(key, key, []byte("abcd"), "")
		}(i)
	}
	wg.Wait()
	if SafeRequestTrace(&RequestTrace{Version: 1, First: RequestAttemptTrace{State: "PRIVATE_PROMPT"}}) != nil {
		t.Fatal("arbitrary metadata accepted")
	}
}

func TestRequestTraceActualCodexAndExplicitZeroCache(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		codexSSE(w, `{"type":"response.output_text.delta","delta":"ok"}`, `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":2,"total_tokens":102,"input_tokens_details":{"cached_tokens":0}}}}`)
	}))
	defer srv.Close()
	inner, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "fake"}})
	if err != nil {
		t.Fatal(err)
	}
	stats := make(chan CallStat, 2)
	p := MeteredConnection(inner, "codex", srv.URL, "fake", PurposeMain, func(s CallStat) { stats <- s })
	ctx := WithOpenCodeSession(context.Background(), t.TempDir())
	first := requestTraceRun(t, p, ctx, stats)
	second := requestTraceRun(t, p, ctx, stats)
	if first.RequestTrace.First.State != "first" || second.RequestTrace.First.State != "identical" || len(bodies) != 2 || bodies[0] != bodies[1] || !second.HasCachedInput || second.TokensCached != 0 {
		t.Fatalf("actual transport telemetry wrong: first=%+v second=%+v", first.RequestTrace, second.RequestTrace)
	}
	u := &Usage{Input: 100, HasCachedInput: true}
	deriveCachedFromTimings(u, &llamaTimings{CacheN: 90})
	if u.CachedInput != 0 {
		t.Fatal("explicit zero overwritten by inferred cache")
	}
}

func BenchmarkRequestTraceFingerprint(b *testing.B) {
	tracker := newRequestTraceTracker()
	key := tracker.digest([]byte("benchmark"))
	body := bytes.Repeat([]byte("x"), 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.compare(key, key, body, "key")
	}
}
