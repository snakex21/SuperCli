package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
)

// Opt-in synthetic evaluation trace. Response bodies only: never persist
// authorization/cookie headers. The reader forwards every original byte and
// error unchanged, retains at most 1 MiB, and writes only when the body closes.
type continuationTraceTransport struct {
	dir  string
	next atomic.Int64
}

func (t *continuationTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	id := t.next.Add(1)
	response.Body = &continuationTraceBody{
		ReadCloser: response.Body,
		path:       filepath.Join(t.dir, fmt.Sprintf("%03d", id)),
		status:     response.StatusCode,
	}
	return response, nil
}

type continuationTraceBody struct {
	io.ReadCloser
	path   string
	status int
	data   []byte
	total  int64
}

func (b *continuationTraceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.total += int64(n)
	keep := min(n, 1024*1024-len(b.data))
	b.data = append(b.data, p[:keep]...)
	return n, err
}

func (b *continuationTraceBody) Close() error {
	closeErr := b.ReadCloser.Close()
	bodyErr := os.WriteFile(b.path+".sse", b.data, 0600)
	meta, _ := json.Marshal(struct {
		Status    int
		Bytes     int64
		Truncated bool
	}{b.status, b.total, int64(len(b.data)) != b.total})
	metaErr := os.WriteFile(b.path+".json", meta, 0600)
	return errors.Join(closeErr, bodyErr, metaErr)
}

func newContinuationTraceClient(outDir string) (*http.Client, error) {
	if os.Getenv("SUPERCLI_CONTINUATION_TRACE") != "1" {
		return nil, nil
	}
	dir := filepath.Join(outDir, "http")
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	return &http.Client{Transport: &continuationTraceTransport{dir: dir}}, nil
}
