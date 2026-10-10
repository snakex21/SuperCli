package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func downloadFixture(t *testing.T, body io.ReadCloser, contentType string, length int64, status int) (*WebDownload, *int) {
	t.Helper()
	tool := NewWebDownload(t.TempDir())
	calls := new(int)
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		(*calls)++
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("download request must be an unauthenticated GET")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, ContentLength: length, Body: body, Request: req}, nil
	})}
	return tool, calls
}

func invokeDownload(t *testing.T, tool *WebDownload, ctx context.Context, path string, maxBytes int64) Result {
	t.Helper()
	args, _ := json.Marshal(webDownloadArgs{URL: "https://assets.example.test/asset?token=never-echo-secret", Path: path, MaxBytes: maxBytes})
	result, err := tool.Spec().Fn(ctx, args)
	if err != nil {
		t.Fatalf("Go-level error: %v", err)
	}
	return result
}

func noDownloadFiles(t *testing.T, tool *WebDownload, path string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(tool.BaseDir, path)); !os.IsNotExist(err) {
		t.Fatalf("failed download published destination: %v", err)
	}
	err := filepath.WalkDir(tool.BaseDir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(entry.Name(), ".supercli-download-") {
			t.Errorf("temporary file left after failure: %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebDownloadStreamsOneRequestToWorkspaceWithCompactMetadata(t *testing.T) {
	body := bytes.Repeat([]byte{0x89, 'P', 'N', 'G', 0, 0xff}, 20_000)
	stream := &observedDownloadReader{Reader: bytes.NewReader(body), t: t}
	tool, calls := downloadFixture(t, stream, "image/png", int64(len(body)), http.StatusOK)
	stream.destination = filepath.Join(tool.BaseDir, "assets", "texture.png")
	result := invokeDownload(t, tool, context.Background(), "assets/texture.png", int64(len(body)))
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	saved, err := os.ReadFile(stream.destination)
	if err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("saved bytes differ: %v", err)
	}
	if *calls != 1 || !stream.closed || stream.maxRead > 32<<10 || len(result.Text) > 350 || result.Image != nil || result.RetainedText != "" {
		t.Fatalf("download must be one bounded stream with compact text: calls=%d max_read=%d closed=%v chars=%d", *calls, stream.maxRead, stream.closed, len(result.Text))
	}
	if !strings.Contains(result.Text, fmt.Sprintf("SHA256: %x", sha256.Sum256(body))) || strings.Contains(result.Text, "never-echo-secret") || strings.Contains(result.Text, "https://") {
		t.Fatalf("unexpected metadata: %s", result.Text)
	}
	if !strings.Contains(result.Text, "Download complete:") || !strings.Contains(result.Text, "Size and SHA256 measured while saving; file ready to use.") {
		t.Fatalf("result must expose completed save/hash evidence: %s", result.Text)
	}
	// A repeated call must fail before a network request and leave bytes intact.
	again := invokeDownload(t, tool, context.Background(), "assets/texture.png", 0)
	if again.Err == nil || !strings.Contains(again.Err.Error(), "destination_exists") || *calls != 1 || again.Text != "" || again.Inert {
		t.Fatalf("existing destination re-downloaded: %+v calls=%d", again, *calls)
	}
	saved, err = os.ReadFile(stream.destination)
	if err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("existing asset changed after refused duplicate: %v", err)
	}
}

type observedDownloadReader struct {
	*bytes.Reader
	t           *testing.T
	destination string
	maxRead     int
	closed      bool
}

func (r *observedDownloadReader) Read(p []byte) (int, error) {
	if _, err := os.Lstat(r.destination); !os.IsNotExist(err) {
		r.t.Fatalf("destination visible before body completed: %v", err)
	}
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	return r.Reader.Read(p)
}

func (r *observedDownloadReader) Close() error { r.closed = true; return nil }

func TestWebDownloadRejectsInvalidInputsBeforeNetwork(t *testing.T) {
	for _, test := range []struct {
		name, rawURL, path string
		limit              int64
	}{
		{"private", "http://127.0.0.1/file", "file.bin", 0},
		{"userinfo", "https://user:secret@example.test/file", "file.bin", 0},
		{"non-http", "file:///secret", "file.bin", 0},
		{"malformed", "https://%zz?token=secret", "file.bin", 0},
		{"escape", "https://example.test/file", "../escape.bin", 0},
		{"empty-path", "https://example.test/file", "", 0},
		{"oversize-limit", "https://example.test/file", "file.bin", webDownloadMaxBytes + 1},
		{"negative-limit", "https://example.test/file", "file.bin", -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader("unused")), "text/plain", -1, http.StatusOK)
			args, _ := json.Marshal(webDownloadArgs{URL: test.rawURL, Path: test.path, MaxBytes: test.limit})
			result, _ := tool.Spec().Fn(context.Background(), args)
			if result.Err == nil || *calls != 0 || strings.Contains(result.Err.Error(), "secret") {
				t.Fatalf("invalid input reached network or leaked URL: %+v calls=%d", result, *calls)
			}
		})
	}
}

func TestWebDownloadNoPublishedOrTemporaryFileAfterFailure(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType string
		length, limit           int64
		status                  int
	}{
		{"known-size", strings.Repeat("x", 1025), "application/octet-stream", 1025, 1024, http.StatusOK},
		{"unknown-size", strings.Repeat("x", 1025), "application/octet-stream", -1, 1024, http.StatusOK},
		{"html", "<!doctype html><html>Login needed</html>", "text/html", -1, 1024, http.StatusOK},
		{"disguised-html", "<!doctype html><html>Not a ZIP</html>", "application/zip", -1, 1024, http.StatusOK},
		{"empty", "", "application/octet-stream", 0, 1024, http.StatusOK},
		{"incomplete", "short", "application/octet-stream", 10, 1024, http.StatusOK},
		{"status", "never-echo-secret", "text/plain", -1, 1024, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader(test.body)), test.contentType, test.length, test.status)
			result := invokeDownload(t, tool, context.Background(), "assets/file.zip", test.limit)
			if result.Err == nil || *calls != 1 || strings.Contains(result.Err.Error(), "never-echo-secret") {
				t.Fatalf("expected one safe failure: %+v calls=%d", result, *calls)
			}
			noDownloadFiles(t, tool, "assets/file.zip")
		})
	}
}

func TestWebDownloadContextCancellationCleansPartialFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelDownloadReader{cancel: cancel}
	tool, calls := downloadFixture(t, reader, "application/octet-stream", -1, http.StatusOK)
	result := invokeDownload(t, tool, ctx, "assets/file.bin", 1024)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "canceled") || *calls != 1 || !reader.closed {
		t.Fatalf("cancellation: %+v calls=%d closed=%v", result, *calls, reader.closed)
	}
	noDownloadFiles(t, tool, "assets/file.bin")
	result = invokeDownload(t, tool, ctx, "assets/file.bin", 1024)
	if result.Err == nil || *calls != 1 {
		t.Fatalf("already canceled context started request: %+v calls=%d", result, *calls)
	}
}

type cancelDownloadReader struct {
	cancel       context.CancelFunc
	done, closed bool
}

func (r *cancelDownloadReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "partial"), nil
}
func (r *cancelDownloadReader) Close() error { r.closed = true; return nil }

func TestWebDownloadTransportErrorDoesNotEchoSignedURL(t *testing.T) {
	tool := NewWebDownload(t.TempDir())
	tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: req.URL.String(), Err: context.DeadlineExceeded}
	})}
	result := invokeDownload(t, tool, context.Background(), "file.bin", 0)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "cause=timeout") || strings.Contains(result.Err.Error(), "never-echo-secret") {
		t.Fatalf("transport error leaked URL: %+v", result)
	}
	noDownloadFiles(t, tool, "file.bin")
}

func TestPublishDownloadNeverOverwritesRacingDestination(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "file.part"), filepath.Join(dir, "file.bin")
	if err := os.WriteFile(from, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := publishDownload(from, to); err == nil {
		t.Fatal("racing destination replaced")
	}
	saved, err := os.ReadFile(to)
	if err != nil || string(saved) != "original" {
		t.Fatalf("existing file changed: %q %v", saved, err)
	}
}

func TestWebDownloadSharedClientBlocksUnsafeRedirects(t *testing.T) {
	tool := NewWebDownload(t.TempDir())
	for _, raw := range []string{"file:///asset", "https://user:secret@example.test/asset"} {
		u, _ := url.Parse(raw)
		if err := tool.client.CheckRedirect(&http.Request{URL: u}, nil); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe redirect: %s %v", raw, err)
		}
	}
	u, _ := url.Parse("https://example.test/file")
	if err := tool.client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, webFetchMaxRedirects)); err == nil {
		t.Fatal("redirect limit missing")
	}
	if err := tool.client.CheckRedirect(&http.Request{URL: u}, nil); err != nil {
		t.Fatal(err)
	}
	if tool.Spec().ReadOnly || tool.client.Timeout != 0 {
		t.Fatal("download marked read-only or given a total runtime limit")
	}
	if transport, ok := tool.client.Transport.(*http.Transport); !ok || transport.Proxy != nil || transport.DialContext == nil || transport.TLSHandshakeTimeout != 10*time.Second || transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatal("download must retain guarded direct connections and bounded connection setup")
	}
}

func TestWebDownloadBodyCanOutliveFormerClientRuntimeLimit(t *testing.T) {
	// Exercise the old total-client-timeout behavior at a short fixture scale;
	// the same body succeeds through the constructor's unlimited total runtime.
	const formerLimit = 20 * time.Millisecond
	for _, limited := range []bool{true, false} {
		name := "caller-owned-runtime"
		if limited {
			name = "former-total-client-limit"
		}
		t.Run(name, func(t *testing.T) {
			tool := NewWebDownload(t.TempDir())
			if limited {
				tool.client.Timeout = formerLimit
			}
			tool.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if !limited {
					if _, deadline := req.Context().Deadline(); deadline {
						t.Fatal("download added a total operation deadline")
					}
				}
				body := &delayedDownloadReader{ctx: req.Context(), delay: 3 * formerLimit}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, ContentLength: 513, Body: body, Request: req}, nil
			})
			result := invokeDownload(t, tool, context.Background(), "assets/slow.bin", 1024)
			if limited {
				if result.Err == nil {
					t.Fatal("fixture did not exercise the former total runtime limit")
				}
				noDownloadFiles(t, tool, "assets/slow.bin")
				return
			}
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			saved, err := os.ReadFile(filepath.Join(tool.BaseDir, "assets", "slow.bin"))
			if err != nil || len(saved) != 513 || saved[512] != 1 {
				t.Fatalf("slow complete body differs: bytes=%d err=%v", len(saved), err)
			}
		})
	}
}

type delayedDownloadReader struct {
	ctx   context.Context
	delay time.Duration
	reads int
}

func (r *delayedDownloadReader) Read(p []byte) (int, error) {
	r.reads++
	switch r.reads {
	case 1:
		return copy(p, make([]byte, 512)), nil
	case 2:
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-timer.C:
			return copy(p, []byte{1}), nil
		}
	default:
		return 0, io.EOF
	}
}

func (r *delayedDownloadReader) Close() error { return nil }

func TestWebDownloadCancelInterruptsBodyAfterPartialFileWasWritten(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tool := NewWebDownload(t.TempDir())
	reader := &blockingDownloadReader{ctx: ctx, ready: make(chan struct{})}
	tool.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, ContentLength: -1, Body: reader, Request: req}, nil
	})
	args, _ := json.Marshal(webDownloadArgs{URL: "https://assets.example.test/asset", Path: "assets/canceled.bin", MaxBytes: 1024})
	done := make(chan Result, 1)
	go func() {
		result, _ := tool.Spec().Fn(ctx, args)
		done <- result
	}()
	select {
	case <-reader.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("body never reached its blocking read")
	}
	parts, err := filepath.Glob(filepath.Join(tool.BaseDir, "assets", ".supercli-download-*.part"))
	if err != nil || len(parts) != 1 {
		t.Fatalf("expected one in-progress partial file: count=%d err=%v", len(parts), err)
	}
	if info, err := os.Stat(parts[0]); err != nil || info.Size() != 512 {
		t.Fatalf("partial bytes were not streamed to disk: info=%v err=%v", info, err)
	}
	cancel()
	select {
	case result := <-done:
		if result.Err == nil || !strings.Contains(result.Err.Error(), "canceled") || !reader.closed {
			t.Fatalf("body cancellation did not stop the download: %+v closed=%v", result, reader.closed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("body did not return after caller cancellation")
	}
	noDownloadFiles(t, tool, "assets/canceled.bin")
}

type blockingDownloadReader struct {
	ctx    context.Context
	ready  chan struct{}
	reads  int
	closed bool
}

func (r *blockingDownloadReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		return copy(p, make([]byte, 512)), nil
	}
	close(r.ready)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *blockingDownloadReader) Close() error { r.closed = true; return nil }

func TestWebDownloadBodyReadFailureDoesNotSaveFile(t *testing.T) {
	tool, _ := downloadFixture(t, io.NopCloser(errorDownloadReader{}), "application/octet-stream", -1, http.StatusOK)
	result := invokeDownload(t, tool, context.Background(), "file.bin", 0)
	if result.Err == nil {
		t.Fatal("body failure accepted")
	}
	noDownloadFiles(t, tool, "file.bin")
}

type errorDownloadReader struct{}

func (errorDownloadReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed with URL?token=never-echo-secret")
}
