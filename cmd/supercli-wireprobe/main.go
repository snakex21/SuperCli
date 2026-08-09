// wireprobe is the verification layer between SuperCli and a real
// OpenAI-compatible model backend (LM Studio, llama.cpp, ...). It is a
// transparent HTTP reverse proxy that records every request body to a
// JSONL file before forwarding.
//
// That recording is what makes the practical checks possible:
//   - reasoning retention (SUPERCLI_KEEP_THINKING): the wire log must
//     contain the "[Retained reasoning ...]" tail on the 2nd+ request
//     of a run, and the assistant history messages must NOT contain
//     <thinking> blocks;
//   - result truncation metadata: tool results that overflow the
//     inline budget must carry "omitted_lines=" in their content;
//   - token/cache sanity: the recorded prompts let us eyeball how the
//     history grows between turns.
//
// Streaming responses (SSE) are forwarded chunk-by-chunk so the agent
// sees token-by-token output exactly like a direct connection.
//
// Usage:
//
//	wireprobe -listen 127.0.0.1:9099 -upstream http://127.0.0.1:1234 -log wire.jsonl
//
// Then point SuperCli at the probe: supercli -batch "..." -base-url http://127.0.0.1:9099
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type requestRecord struct {
	Time    string `json:"time"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Query   string `json:"query,omitempty"`
	Status  int    `json:"status"`
	BodyLen int    `json:"bodyLen"`
	Body    string `json:"body"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9099", "probe listen address")
	upstream := flag.String("upstream", "http://127.0.0.1:1234", "upstream OpenAI-compatible base URL")
	logPath := flag.String("log", "wire.jsonl", "JSONL request log")
	flag.Parse()

	up, err := url.Parse(*upstream)
	if err != nil {
		fatal(fmt.Sprintf("bad upstream %q: %v", *upstream, err))
	}

	f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fatal(fmt.Sprintf("open log: %v", err))
	}
	defer f.Close()

	var mu sync.Mutex
	record := func(rec requestRecord) {
		b, err := json.Marshal(rec)
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = f.Write(append(b, '\n'))
		_ = f.Sync()
	}

	client := &http.Client{Timeout: 0}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "wireprobe: read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		r.Body.Close()

		// Forward.
		target := *up
		target.Path = r.URL.Path
		target.RawQuery = r.URL.RawQuery
		req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, "wireprobe: build request: "+err.Error(), http.StatusBadGateway)
			return
		}
		for k, vs := range r.Header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Host", up.Host)
		req.Header.Set("Accept-Encoding", "identity") // keep the body inspectable end-to-end

		resp, err := client.Do(req)
		if err != nil {
			record(requestRecord{Time: time.Now().UTC().Format(time.RFC3339Nano), Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Status: 0, BodyLen: len(body), Body: string(body)})
			http.Error(w, "wireprobe: upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream the response back in chunks so SSE arrives live.
		buf := make([]byte, 32*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, err := w.Write(buf[:n]); err != nil {
					break
				}
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
			}
			if readErr != nil {
				break
			}
		}

		record(requestRecord{Time: time.Now().UTC().Format(time.RFC3339Nano), Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Status: resp.StatusCode, BodyLen: len(body), Body: string(body)})
	})

	fmt.Printf("wireprobe: listening on %s -> %s, log %s\n", *listen, up.String(), *logPath)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "wireprobe:", msg)
	os.Exit(1)
}
