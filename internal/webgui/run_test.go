package webgui

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestLocalLaunchURL(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0:8765":  "http://127.0.0.1:8765/",
		"[::]:8765":     "http://[::1]:8765/",
		"127.0.0.1:12":  "http://127.0.0.1:12/",
		"[::1]:34":      "http://[::1]:34/",
		"localhost:567": "http://localhost:567/",
	}
	for in, want := range tests {
		if got := localLaunchURL(in); got != want {
			t.Errorf("localLaunchURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShutdownAfterWindowCloseForcesLingeringRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release // Deliberately ignore request cancellation.
	})}
	go func() { _ = srv.Serve(ln) }()
	go func() { _, _ = http.Get("http://" + ln.Addr().String()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach test server")
	}
	started := time.Now()
	if err := shutdownAfterWindowClose(srv); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("window shutdown took %v; want a bounded fast exit", elapsed)
	}
}
