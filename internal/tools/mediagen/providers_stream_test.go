package mediagen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFalStatusStreamURLPreservesScopeAndEscaping(t *testing.T) {
	base, _ := url.Parse("https://queue.fal.run")
	tool := NewVideo(".", nil, nil)
	rc := runtimeConfig{base: base}
	for _, tc := range []struct{ raw, want string }{
		{"https://queue.fal.run/jobs/job/status", "https://queue.fal.run/jobs/job/status/stream"},
		{"https://queue.fal.run/jobs/job/status/", "https://queue.fal.run/jobs/job/status/stream"},
		{"https://queue.fal.run/jobs/job/status%2F", "https://queue.fal.run/jobs/job/status%2F/stream"},
		{"https://queue.fal.run:443/jobs/%2Fjob/status?logs=0", "https://queue.fal.run:443/jobs/%2Fjob/status/stream?logs=0"},
	} {
		got, err := tool.statusStreamURL(rc, tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("stream URL=%q err=%v want=%q", got, err, tc.want)
		}
	}
	for _, raw := range []string{
		"https://foreign.example/jobs/status", "https://queue.fal.run:444/jobs/status",
		"http://queue.fal.run/jobs/status", "https://user:fixture@queue.fal.run/jobs/status",
		"https://queue.fal.run/jobs/status#fragment",
	} {
		if _, err := tool.statusStreamURL(rc, raw); err == nil {
			t.Fatal("accepted untrusted stream URL")
		}
	}
}

func TestFalStatusStreamFrames(t *testing.T) {
	for _, tc := range []struct {
		name, stream, wantError string
	}{
		{"completed", "data: {\"status\":\"COMPLETED\"}\n\n", ""},
		{"sequence", "data: {\"status\":\"IN_QUEUE\",\"request_id\":\"job\"}\n\ndata: {\"status\":\"IN_PROGRESS\",\"request_id\":\"job\"}\n\ndata: {\"status\":\"COMPLETED\",\"request_id\":\"job\"}\n\n", ""},
		{"crlf-comments-multiline", ": heartbeat\r\nretry: 1\r\nid: inert\r\nevent: status\r\ndata: {\"status\":\r\ndata: \"COMPLETED\",\"request_id\":\"job\"}\r\n\r\n", ""},
		{"early-eof", "data: {\"status\":\"IN_PROGRESS\"}\n\n", "ended before completion"},
		{"unterminated", "data: {\"status\":\"COMPLETED\"}\n", "ended before completion"},
		{"invalid-json", "data: private provider text\n\n", "invalid status JSON"},
		{"unknown", "data: {\"status\":\"MYSTERY\"}\n\n", "unknown queue status"},
		{"missing-status", "data: null\n\n", "unknown queue status"},
		{"mismatched", "data: {\"status\":\"COMPLETED\",\"request_id\":\"other\"}\n\n", "does not match"},
		{"intermediate-error", "data: {\"status\":\"IN_PROGRESS\",\"error\":\"private provider text\"}\n\n", "details withheld"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Key fixture" {
					t.Error("wrong status stream request")
				}
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, tc.stream)
			}))
			defer server.Close()
			status, err := waitFalCompletion(context.Background(), server.Client(), server.URL, "Key fixture", "job")
			if tc.wantError == "" {
				if err != nil || status.Status != "COMPLETED" {
					t.Fatalf("status=%+v err=%v", status, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "private provider text") {
				t.Fatalf("error=%v want=%q", err, tc.wantError)
			}
			if calls.Load() != 1 {
				t.Fatalf("opened %d streams", calls.Load())
			}
		})
	}
}

func TestFalStatusStreamReturnsAndClosesAtTerminalFrame(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"status\":\"COMPLETED\",\"request_id\":\"job\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := waitFalCompletion(ctx, server.Client(), server.URL, "Key fixture", "job"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("terminal frame did not close stream before EOF")
	}
}

func TestFalStatusStreamBounds(t *testing.T) {
	for _, mode := range []string{"long-line", "multi-line-event", "total-stream", "declared-size"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				switch mode {
				case "long-line":
					_, _ = fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("x", int(maxMetadataBytes)+1))
				case "multi-line-event":
					line := "data: " + strings.Repeat("x", 4096) + "\n"
					for i := int64(0); i <= maxMetadataBytes/4096; i++ {
						if _, err := io.WriteString(w, line); err != nil {
							return
						}
					}
					_, _ = io.WriteString(w, "\n")
				case "total-stream":
					line := ":" + strings.Repeat("x", 4094) + "\n"
					for i := int64(0); i <= maxFalStatusStreamBytes/int64(len(line)); i++ {
						if _, err := io.WriteString(w, line); err != nil {
							return
						}
					}
				case "declared-size":
					w.Header().Set("Content-Length", fmt.Sprint(maxFalStatusStreamBytes+1))
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()
			_, err := waitFalCompletion(context.Background(), server.Client(), server.URL, "Key fixture", "job")
			if err == nil {
				t.Fatal("oversized status stream accepted")
			}
			if mode != "long-line" && !strings.Contains(err.Error(), "byte limit") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestFalStreamFailureDoesNotReconnectResubmitOrFetchResult(t *testing.T) {
	for _, mode := range []string{"early-eof", "invalid-json", "unterminated", "content-type", "http-error", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var foreignCalls atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
			defer trap.Close()
			f := &videoFixture{}
			baseHandler := f.handler(t)
			tool := configuredTool(t, "video", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/jobs/job-123/status/stream" {
					baseHandler(w, r)
					return
				}
				f.streams.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				switch mode {
				case "early-eof":
					_, _ = io.WriteString(w, "data: {\"status\":\"IN_PROGRESS\"}\n\n")
				case "invalid-json":
					_, _ = io.WriteString(w, "data: private provider text\n\n")
				case "unterminated":
					_, _ = io.WriteString(w, "data: {\"status\":\"COMPLETED\"}\n")
				case "content-type":
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, "{\"status\":\"COMPLETED\"}")
				case "http-error":
					http.Error(w, "private provider text", http.StatusBadGateway)
				case "redirect":
					http.Redirect(w, r, trap.URL, http.StatusFound)
				}
			})
			_, err := run(t, tool, `{"prompt":"test"}`)
			if err == nil || strings.Contains(err.Error(), "private provider text") || strings.Contains(err.Error(), "fixture-only-secret") {
				t.Fatalf("unsafe/missing error: %v", err)
			}
			if f.submits.Load() != 1 || f.streams.Load() != 1 || f.cancels.Load() != 1 || f.responses.Load() != 0 || f.downloads.Load() != 0 || foreignCalls.Load() != 0 {
				t.Fatalf("submit=%d stream=%d cancel=%d result=%d download=%d foreign=%d", f.submits.Load(), f.streams.Load(), f.cancels.Load(), f.responses.Load(), f.downloads.Load(), foreignCalls.Load())
			}
			assertNoOutput(t, tool)
		})
	}
}

func TestFalStatusStreamCancellationWithoutData(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := waitFalCompletion(ctx, server.Client(), server.URL, "Key fixture", "job")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
