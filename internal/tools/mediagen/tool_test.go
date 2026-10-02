package mediagen

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/tools/core"
)

//go:embed testdata/preview.png
var tinyPNG []byte

//go:embed testdata/preview.mp4
var tinyMP4 []byte

func configuredTool(t *testing.T, kind string, handler http.HandlerFunc) *Tool {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, model := "openai", "test-image"
	if kind == "video" {
		provider, model = "fal", "fal-ai/test/video"
	}
	cfg := &config.MediaGenerationProviderConf{Enabled: true, Provider: provider, BaseURL: server.URL, Model: model, APIKeyEnv: "MEDIA_FIXTURE_KEY", AllowedDownloadHosts: []string{"127.0.0.1"}}
	tool := newTool(t.TempDir(), kind, cfg, func(context.Context, string) error { return nil })
	tool.client = server.Client()
	tool.allowTestHTTP = true
	tool.pollOverride = time.Millisecond
	tool.lookupEnv = func(name string) string {
		if name != "MEDIA_FIXTURE_KEY" {
			t.Errorf("unexpected environment access %q", name)
		}
		return "fixture-only-secret"
	}
	return tool
}
func run(t *testing.T, tool *Tool, args string) (core.Result, error) {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Go-level error: %v", err)
	}
	return res, res.Err
}
func assertNoOutput(t *testing.T, tool *Tool) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(tool.baseDir, "generated"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial files retained: %v", entries)
	}
}
func imageResponse(w http.ResponseWriter, data []byte) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
}

func TestImageSuccessRequiresConfirmationAndPersistsMetadata(t *testing.T) {
	if _, err := png.Decode(bytes.NewReader(tinyPNG)); err != nil {
		t.Fatalf("PNG success fixture is not displayable: %v", err)
	}
	var approved atomic.Bool
	var requests atomic.Int32
	tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !approved.Load() {
			t.Error("request preceded confirmation")
		}
		if r.Method != "POST" || r.URL.Path != "/images/generations" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-only-secret" {
			t.Error("missing scoped bearer key")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["prompt"] != "A blue bird" || body["model"] != "test-image" || body["n"] != float64(1) {
			t.Errorf("body=%v", body)
		}
		if _, ok := body["response_format"]; ok {
			t.Error("deprecated response_format sent")
		}
		imageResponse(w, tinyPNG)
	})
	tool.confirm = func(_ context.Context, q string) error {
		if !strings.Contains(q, "A blue bird") || !strings.Contains(q, tool.cfg.BaseURL) || strings.Contains(q, "fixture-only-secret") {
			t.Errorf("unsafe/missing confirmation: %s", q)
		}
		approved.Store(true)
		return nil
	}
	res, err := run(t, tool, `{"prompt":"A blue bird"}`)
	if err != nil {
		t.Fatal(err)
	}
	var meta Output
	if err = json.Unmarshal([]byte(res.Text), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Type != "image" || meta.MediaType != "image/png" || meta.Bytes != int64(len(tinyPNG)) || meta.Attached || res.Image != nil || len(res.Images) != 0 {
		t.Fatalf("bad result: %+v / %+v", meta, res)
	}
	data, err := os.ReadFile(meta.Path)
	if err != nil || !bytes.Equal(data, tinyPNG) {
		t.Fatalf("saved %q %v", data, err)
	}
	res2, err := run(t, tool, `{"prompt":"A blue bird"}`)
	if err != nil {
		t.Fatal(err)
	}
	var meta2 Output
	_ = json.Unmarshal([]byte(res2.Text), &meta2)
	if meta.Path == meta2.Path {
		t.Fatal("reused/overwrote output")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestGenerationDisabledOrUnconfirmedNeverSends(t *testing.T) {
	for _, mode := range []string{"disabled", "nil-config", "missing-confirm", "denied", "missing-key", "invalid-provider", "invalid-url", "model-url"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) { requests.Add(1); imageResponse(w, tinyPNG) })
			switch mode {
			case "disabled":
				tool.cfg.Enabled = false
			case "nil-config":
				tool.cfg = nil
			case "missing-confirm":
				tool.confirm = nil
			case "denied":
				tool.confirm = func(context.Context, string) error { return errors.New("declined") }
			case "missing-key":
				tool.lookupEnv = func(string) string { return "" }
			case "invalid-provider":
				tool.cfg.Provider = "sora"
			case "invalid-url":
				tool.cfg.BaseURL = "http://localhost"
				tool.allowTestHTTP = false
			case "model-url":
				tool.cfg.Model = "https://evil.test/steal"
			}
			if _, err := run(t, tool, `{"prompt":"test"}`); err == nil {
				t.Fatal("expected rejection")
			}
			if requests.Load() != 0 {
				t.Fatal("network request before valid config/approval")
			}
			assertNoOutput(t, tool)
		})
	}
}

func TestArgumentsAndParameterAllowlist(t *testing.T) {
	var requests atomic.Int32
	tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["quality"] != "low" || body["size"] != "1024x1024" {
			t.Errorf("body=%v", body)
		}
		imageResponse(w, tinyPNG)
	})
	tool.cfg.AllowedParameters = []string{"quality", "size"}
	tool.cfg.DefaultParameters = map[string]any{"quality": "low"}
	if _, err := run(t, tool, `{"prompt":"test","parameters":{"size":"1024x1024"}}`); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"prompt":""}`, `{"prompt":"test","endpoint":"https://evil.test"}`, `{"prompt":"test","parameters":{"n":5}}`, `{"prompt":"test","parameters":{"model":"other"}}`, `{"prompt":"test","parameters":{"size":4}}`, `{"prompt":"test"} {}`, `not json`} {
		if _, err := run(t, tool, raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if requests.Load() != 1 {
		t.Errorf("unexpected requests %d", requests.Load())
	}
	tool.cfg.AllowedParameters = []string{"webhook_url"}
	if _, err := run(t, tool, `{"prompt":"test"}`); err == nil {
		t.Fatal("accepted unsafe configured parameter")
	}
}

func TestImageProviderErrorsOversizeAndMalformedOutput(t *testing.T) {
	for _, mode := range []string{"http-error", "invalid-json", "missing-image", "multiple-images", "provider-error", "bad-base64", "html", "too-large", "large-envelope"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch mode {
				case "http-error":
					http.Error(w, "fixture-only-secret: internal details", http.StatusTooManyRequests)
				case "invalid-json":
					fmt.Fprint(w, "no")
				case "missing-image":
					fmt.Fprint(w, `{"data":[]}`)
				case "multiple-images":
					fmt.Fprint(w, `{"data":[{"b64_json":"a"},{"b64_json":"b"}]}`)
				case "provider-error":
					fmt.Fprint(w, `{"error":{"message":"fixture-only-secret"}}`)
				case "bad-base64":
					io.WriteString(w, `{"data":[{"b64_json":"%%%bad%%%"}]}`)
				case "html":
					imageResponse(w, []byte("<html>not an image</html>"))
				case "too-large":
					imageResponse(w, append(append([]byte(nil), tinyPNG...), bytes.Repeat([]byte("x"), 1024)...))
				case "large-envelope":
					w.Header().Set("Content-Length", "999999999")
				}
			})
			tool.cfg.MaxBytes = 64
			if _, err := run(t, tool, `{"prompt":"test"}`); err == nil {
				t.Fatal("expected error")
			} else if strings.Contains(err.Error(), "fixture-only-secret") {
				t.Fatal("leaked provider error/key")
			}
			if requests.Load() != 1 {
				t.Errorf("retry count %d", requests.Load())
			}
			assertNoOutput(t, tool)
		})
	}
}

type videoFixture struct {
	polls     atomic.Int32
	submits   atomic.Int32
	cancels   atomic.Int32
	downloads atomic.Int32
	mode      string
	foreign   string
	onStatus  func()
}

func (f *videoFixture) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		if r.URL.Path == "/media/result.mp4" {
			f.downloads.Add(1)
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("credential leaked to media download")
			}
			switch f.mode {
			case "download-redirect":
				http.Redirect(w, r, f.foreign, http.StatusFound)
			case "download-html":
				fmt.Fprint(w, "<html>no media</html>")
			case "download-oversize":
				w.Header().Set("Content-Length", "1000000")
			case "stream-oversize":
				w.Header().Set("Content-Type", "video/mp4")
				w.(http.Flusher).Flush()
				w.Write(append(append([]byte(nil), tinyMP4...), bytes.Repeat([]byte("x"), 1024)...))
			default:
				w.Write(tinyMP4)
			}
			return
		}
		if r.Header.Get("Authorization") != "Key fixture-only-secret" {
			t.Errorf("missing fal key for %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/fal-ai/test/video":
			f.submits.Add(1)
			if r.Method != "POST" {
				t.Error("submit must POST")
			}
			urls := map[string]any{"request_id": "job-123", "status_url": base + "/jobs/job-123/status", "response_url": base + "/jobs/job-123/response", "cancel_url": base + "/jobs/job-123/cancel"}
			switch f.mode {
			case "foreign-status":
				urls["status_url"] = f.foreign
			case "foreign-response":
				urls["response_url"] = f.foreign
			case "foreign-cancel":
				urls["cancel_url"] = f.foreign
			}
			_ = json.NewEncoder(w).Encode(urls)
		case "/jobs/job-123/status":
			n := f.polls.Add(1)
			if f.onStatus != nil {
				f.onStatus()
			}
			status := "COMPLETED"
			if n == 1 {
				status = "IN_QUEUE"
			} else if n == 2 {
				status = "IN_PROGRESS"
			}
			if f.mode == "timeout" || f.mode == "cancel" {
				status = "IN_PROGRESS"
			}
			if f.mode == "unknown-status" {
				status = "MYSTERY"
			}
			if f.mode == "provider-failure" {
				fmt.Fprint(w, `{"status":"COMPLETED","error":"fixture-only-secret"}`)
				return
			}
			if f.mode == "mismatched-id" {
				fmt.Fprint(w, `{"status":"IN_QUEUE","request_id":"another"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "request_id": "job-123"})
		case "/jobs/job-123/response":
			if f.mode == "result-failure" {
				http.Error(w, "private", 500)
				return
			}
			link := base + "/media/result.mp4"
			if f.mode == "foreign-download" {
				link = "https://untrusted.example/video.mp4"
			}
			if f.mode == "private-download" {
				link = "https://169.254.169.254/video.mp4"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"video": map[string]any{"url": link}})
		case "/jobs/job-123/cancel":
			f.cancels.Add(1)
			if r.Method != "PUT" {
				t.Errorf("cancel method=%s", r.Method)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"status":"CANCELLATION_REQUESTED"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}
}
func TestFalQueueSuccessAndUnauthenticatedDownload(t *testing.T) {
	f := &videoFixture{}
	tool := configuredTool(t, "video", f.handler(t))
	res, err := run(t, tool, `{"prompt":"A bird flies"}`)
	if err != nil {
		t.Fatal(err)
	}
	var meta Output
	_ = json.Unmarshal([]byte(res.Text), &meta)
	if meta.Type != "video" || meta.MediaType != "video/mp4" || meta.Bytes != int64(len(tinyMP4)) || meta.Attached || res.Image != nil {
		t.Errorf("result=%+v", meta)
	}
	data, err := os.ReadFile(meta.Path)
	if err != nil || !bytes.Equal(data, tinyMP4) {
		t.Fatalf("saved file: %v", err)
	}
	if f.submits.Load() != 1 || f.polls.Load() != 3 || f.cancels.Load() != 0 || f.downloads.Load() != 1 {
		t.Fatalf("counts submit=%d polls=%d cancel=%d download=%d", f.submits.Load(), f.polls.Load(), f.cancels.Load(), f.downloads.Load())
	}
}
func TestFalFailureURLAndDownloadBoundaries(t *testing.T) {
	for _, mode := range []string{"foreign-status", "foreign-response", "foreign-cancel", "foreign-download", "private-download", "unknown-status", "mismatched-id", "provider-failure", "result-failure", "download-html", "download-oversize", "stream-oversize", "download-redirect"} {
		t.Run(mode, func(t *testing.T) {
			var foreignCalls atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
			defer trap.Close()
			f := &videoFixture{mode: mode, foreign: trap.URL + "/steal"}
			tool := configuredTool(t, "video", f.handler(t))
			tool.cfg.MaxBytes = 64
			_, err := run(t, tool, `{"prompt":"test"}`)
			if err == nil {
				t.Fatal("expected failure")
			}
			if strings.Contains(err.Error(), "fixture-only-secret") {
				t.Fatal("leaked credential/error body")
			}
			if foreignCalls.Load() != 0 {
				t.Fatal("followed untrusted endpoint or redirect")
			}
			if f.submits.Load() != 1 {
				t.Fatal("resubmitted job")
			}
			assertNoOutput(t, tool)
			if (mode == "foreign-status" || mode == "foreign-response" || mode == "unknown-status" || mode == "mismatched-id") && f.cancels.Load() != 1 {
				t.Errorf("did not best-effort cancel: %v", err)
			}
			if mode == "foreign-cancel" && f.cancels.Load() != 0 {
				t.Fatal("used untrusted cancel endpoint")
			}
		})
	}
}
func TestFalTimeoutAndUserCancelBestEffortRemoteCancel(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := &videoFixture{mode: mode}
			tool := configuredTool(t, "video", f.handler(t))
			tool.timeoutOverride = 30 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				f.onStatus = cancel
				tool.timeoutOverride = time.Second
			}
			res, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			expected := context.DeadlineExceeded
			if mode == "cancel" {
				expected = context.Canceled
			}
			if !errors.Is(res.Err, expected) {
				t.Fatalf("error=%v, expected %v", res.Err, expected)
			}
			if f.cancels.Load() != 1 || f.submits.Load() != 1 {
				t.Fatalf("submit=%d cancel=%d", f.submits.Load(), f.cancels.Load())
			}
			if !strings.Contains(res.Err.Error(), "may still finish and charge") {
				t.Error("cancellation warning missing")
			}
			assertNoOutput(t, tool)
		})
	}
}

func TestNetworkPrivateAndSpecialIPRejection(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "192.168.2.3", "172.16.0.1", "100.64.0.1", "0.0.0.0", "192.0.2.1", "198.18.0.1", "224.0.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::7f00:1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("rejected public %s", ip)
		}
	}
	for _, raw := range []string{"http://api.openai.com", "https://key@api.openai.com", "https://localhost", "https://127.0.0.1", "https://[::1]", "https://api.openai.com/#fragment", "file:///etc/passwd"} {
		if _, err := strictURL(raw, false); err == nil {
			t.Errorf("accepted URL %s", raw)
		}
	}
}
func TestAuthenticatedURLsRequireExactOrigin(t *testing.T) {
	base, _ := url.Parse("https://queue.fal.run")
	tool := NewVideo(".", nil, nil)
	rc := runtimeConfig{base: base}
	for _, raw := range []string{"https://queue.fal.run.evil.test/jobs", "https://evil.test/jobs", "https://queue.fal.run:444/jobs", "http://queue.fal.run/jobs", "https://user:secret@queue.fal.run/jobs"} {
		if _, err := tool.authenticatedURL(rc, raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := tool.authenticatedURL(rc, "https://queue.fal.run:443/jobs/id/status"); err != nil {
		t.Fatal(err)
	}
}
func TestOutputRejectsSymlinkEscapeAndCleansPartial(t *testing.T) {
	tool := NewImage(t.TempDir(), nil, nil)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(tool.baseDir, "generated")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := tool.saveOutput(context.Background(), bytes.NewReader(tinyPNG), 1024); err == nil {
		t.Fatal("escaped workspace")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside workspace")
	}
}
func TestToolSchemaDiscoverableAndConfigSnapshot(t *testing.T) {
	cfg := &config.MediaGenerationProviderConf{Enabled: true, Provider: "openai", Model: "original", AllowedParameters: []string{"quality"}, DefaultParameters: map[string]any{"quality": "low"}}
	tool := NewImage(".", cfg, nil)
	cfg.Model = "mutated"
	cfg.AllowedParameters[0] = "webhook_url"
	cfg.DefaultParameters["quality"] = "high"
	if tool.cfg.Model != "original" || tool.cfg.AllowedParameters[0] != "quality" || tool.cfg.DefaultParameters["quality"] != "low" {
		t.Fatal("shared mutable config")
	}
	for _, candidate := range []*Tool{tool, NewVideo(".", nil, nil)} {
		spec := candidate.Spec()
		if err := spec.Validate(); err != nil {
			t.Fatal(err)
		}
		if spec.ReadOnly {
			t.Fatal("generation marked read-only")
		}
		registry := core.NewRegistry()
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
}
func BenchmarkDisabledMediaToolConstruction(b *testing.B) {
	for b.Loop() {
		tool := NewImage(".", nil, nil)
		if tool.cfg != nil {
			b.Fatal("unexpected config")
		}
	}
}

func TestImageDeadlineStopsWaitingWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			t.Error("request context was not cancelled")
		}
	})
	tool.timeoutOverride = 20 * time.Millisecond
	_, err := run(t, tool, `{"prompt":"test"}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
	assertNoOutput(t, tool)
}

func TestImageExactLimitAndOutputFailureCleanup(t *testing.T) {
	tool := NewImage(t.TempDir(), nil, nil)
	res, err := tool.saveOutput(context.Background(), bytes.NewReader(tinyPNG), int64(len(tinyPNG)))
	if err != nil {
		t.Fatal(err)
	}
	var meta Output
	_ = json.Unmarshal([]byte(res.Text), &meta)
	if meta.Bytes != int64(len(tinyPNG)) {
		t.Fatalf("bytes=%d", meta.Bytes)
	}
	// Valid magic with a deliberately interrupted oversized stream tests removal
	// after the file has already been opened and partially written.
	tool = NewImage(t.TempDir(), nil, nil)
	payload := append(append([]byte(nil), tinyPNG...), bytes.Repeat([]byte("x"), 2048)...)
	if _, err = tool.saveOutput(context.Background(), bytes.NewReader(payload), 600); err == nil {
		t.Fatal("accepted oversized stream")
	}
	assertNoOutput(t, tool)
	tool = NewImage(t.TempDir(), nil, nil)
	interrupted := io.MultiReader(bytes.NewReader(payload[:512]), errorReader{})
	if _, err = tool.saveOutput(context.Background(), interrupted, 4096); err == nil {
		t.Fatal("accepted interrupted output")
	}
	assertNoOutput(t, tool)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("fixture stream interrupted") }

func TestInvalidConfigurationFailsBeforeApproval(t *testing.T) {
	for _, mode := range []string{"max-bytes", "timeout", "poll", "key-name", "key-controls", "model-traversal", "host-wildcard", "empty-download-hosts"} {
		t.Run(mode, func(t *testing.T) {
			tool := configuredTool(t, "video", func(w http.ResponseWriter, r *http.Request) { t.Error("invalid config made request") })
			tool.confirm = func(context.Context, string) error { t.Error("asked approval for invalid config"); return nil }
			switch mode {
			case "max-bytes":
				tool.cfg.MaxBytes = MaxOutputBytes + 1
			case "timeout":
				tool.cfg.TimeoutSeconds = 3601
			case "poll":
				tool.cfg.PollIntervalMilliseconds = 249
			case "key-name":
				tool.cfg.APIKeyEnv = "KEY;OTHER"
			case "key-controls":
				tool.lookupEnv = func(string) string { return "key\r\nInjected: yes" }
			case "model-traversal":
				tool.cfg.Model = "fal-ai/../another"
			case "host-wildcard":
				tool.cfg.AllowedDownloadHosts = []string{"*.fal.media"}
			case "empty-download-hosts":
				tool.cfg.AllowedDownloadHosts = nil
			}
			if _, err := run(t, tool, `{"prompt":"test"}`); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestImageAuthenticatedRedirectNeverFollowed(t *testing.T) {
	var trapRequests atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trapRequests.Add(1) }))
	defer trap.Close()
	tool := configuredTool(t, "image", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trap.URL, http.StatusTemporaryRedirect)
	})
	if _, err := run(t, tool, `{"prompt":"test"}`); err == nil {
		t.Fatal("expected redirect rejection")
	}
	if trapRequests.Load() != 0 {
		t.Fatal("followed authenticated redirect")
	}
}

func TestDeprecatedIPv6SpecialRangesAreNotPublic(t *testing.T) {
	for _, address := range []string{"fec0::1", "::192.168.0.1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Fatalf("accepted non-public address %s", address)
		}
	}
}
