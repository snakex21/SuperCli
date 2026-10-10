package agent

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompactionRuntimeHTTPTracePreservesWireAndBlocksRetry(t *testing.T) {
	const input = `{"model":"unrelated-model","reasoning_effort":"xhigh","messages":[],"stream":true}`
	var received string
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		received = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	wire := &runtimeCompactionTransport{inner: transport, host: u.Host}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/chat/completions", strings.NewReader(input))
	response, err := wire.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !bytes.Equal(content, []byte("data: [DONE]\n\n")) || received != input {
		t.Fatalf("diagnostic altered request/response: %v %q %q", err, received, content)
	}
	first := wire.requests[0]
	if first.ConnectionReadyNS == nil || first.RequestWrittenNS == nil || first.FirstResponseByteNS == nil || first.ResponseHeadersNS == nil ||
		*first.ConnectionReadyNS < 0 || *first.FirstResponseByteNS < *first.ConnectionReadyNS || *first.ResponseHeadersNS < *first.FirstResponseByteNS ||
		string(first.Body) != input || first.HTTPStatus != http.StatusOK || first.Blocked {
		t.Fatalf("invalid observed HTTP boundaries: %+v", first)
	}
	retry, _ := http.NewRequest(http.MethodPost, server.URL+"/chat/completions", strings.NewReader(input))
	if _, err := wire.RoundTrip(retry); err == nil || calls.Load() != 1 || len(wire.requests) != 2 || !wire.requests[1].Blocked || wire.requests[1].ConnectionReadyNS != nil {
		t.Fatal("diagnostic sent a compatibility retry or manufactured network timing")
	}
}
