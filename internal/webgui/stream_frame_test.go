package webgui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseParityEvents() []wireEvent {
	return []wireEvent{
		{Type: "message", Text: "Zażółć 中文 😀 <>&\u2028\u2029\nquote\" slash\\"},
		{Type: "reasoning", Text: "\xff\xfe\x00"},
		{Type: "tool_result", Output: strings.Repeat("large \noutput ", 10000)},
		{Type: "reasoning", ReasoningTok: 123}, {Type: "done", TokIn: 10, TokOut: 2},
		{Type: "error", Err: "problem"}, {Type: "worker_progress", ID: "worker", Prompt: "work", Output: "finished"},
	}
}

func sseHTTPTestServer(t *testing.T, protocol int, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	if protocol == 2 {
		server.EnableHTTP2 = true
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	return server
}

func TestSSEFrameHTTPParity(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run(fmt.Sprintf("HTTP%d", protocol), func(t *testing.T) { testSSEFrameHTTPParity(t, protocol) })
	}
}

func testSSEFrameHTTPParity(t *testing.T, protocol int) {
	var expected bytes.Buffer
	for _, ev := range sseParityEvents() {
		fmt.Fprintf(&expected, "data: %s\n\n", ev.marshal())
	}
	server := sseHTTPTestServer(t, protocol, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range sseParityEvents() {
			writeSSEFrame(w, ev.marshal())
			w.(http.Flusher).Flush()
		}
	})
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != protocol {
		t.Fatalf("HTTP protocol %d, want %d", resp.ProtoMajor, protocol)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, expected.Bytes()) {
		t.Fatal("HTTP body differs from legacy SSE framing")
	}
}

func TestSSEFrameFirstEventFlush(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run(fmt.Sprintf("HTTP%d", protocol), func(t *testing.T) { testSSEFrameFirstEventFlush(t, protocol) })
	}
}

func testSSEFrameFirstEventFlush(t *testing.T, protocol int) {
	proceed := make(chan struct{})
	server := sseHTTPTestServer(t, protocol, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEFrame(w, wireEvent{Type: "message", Text: "first"}.marshal())
		w.(http.Flusher).Flush()
		<-proceed
		writeSSEFrame(w, wireEvent{Type: "done"}.marshal())
		w.(http.Flusher).Flush()
	})
	// Always release the waiting handler, including a failing client read.
	released := false
	defer func() {
		if !released {
			close(proceed)
		}
	}()
	client := server.Client()
	client.Timeout = 5e9
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != protocol {
		t.Fatalf("HTTP protocol %d, want %d", resp.ProtoMajor, protocol)
	}
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if first != "data: "+string((wireEvent{Type: "message", Text: "first"}).marshal())+"\n" || second != "\n" {
		t.Fatal("first frame was not complete before next event")
	}
	close(proceed)
	released = true
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "data: "+string((wireEvent{Type: "done"}).marshal())+"\n\n" {
		t.Fatal("terminal frame changed")
	}
}

type sseFailWriter struct {
	calls  int
	failAt int
	short  bool
}

func (w *sseFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.failAt {
		if w.short {
			return max(0, len(p)-1), nil
		}
		return 0, errors.New("write failed")
	}
	return len(p), nil
}
func (w *sseFailWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func TestSSEFrameStopsAfterWriteFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		for _, at := range []int{1, 2, 3} {
			w := &sseFailWriter{failAt: at, short: short}
			writeSSEFrame(w, []byte("{\"type\":\"message\"}"))
			if w.calls != at {
				t.Fatalf("continued writes after failure at %d", at)
			}
		}
	}
}

func BenchmarkSSEFrameLoopback(b *testing.B) {
	for _, size := range []int{32, 4096, 131072, 1048576} {
		for _, variant := range []string{"printf", "direct"} {
			b.Run(fmt.Sprintf("%d/%s", size, variant), func(b *testing.B) {
				ev := wireEvent{Type: "tool_result", ID: "call", Output: strings.Repeat("x", size)}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, event := range []wireEvent{{Type: "message", Text: "first"}, ev, {Type: "done"}} {
						if variant == "printf" {
							fmt.Fprintf(w, "data: %s\n\n", event.marshal())
						} else {
							writeSSEFrame(w, event.marshal())
						}
						w.(http.Flusher).Flush()
					}
				}))
				defer server.Close()
				client := server.Client()
				client.Timeout = 5e9
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					resp, err := client.Get(server.URL)
					if err != nil {
						b.Fatal(err)
					}
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
			})
		}
	}
}
