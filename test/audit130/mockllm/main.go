//go:build audit130

// Command mockllm is a test-only local LLM server used by the 1.0.30 robustness
// audit to reproduce degenerate tool-call argument streams against the real
// SuperCli binary without any paid provider. It serves:
//
//	POST /v1/chat/completions  (Chat Completions SSE)
//	POST /v1/responses         (Responses SSE)
//	GET  /v1/models            (static list)
//
// The first request that advertises tools (up to -degen-count requests) gets a
// degenerate tool call (-mode); every later request gets a short "done" text
// answer so the agent can finish. It binds loopback only and never logs the
// Authorization header.
//
// Build: go build -buildvcs=false -tags audit130 -o mockllm.exe ./test/audit130/mockllm
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	flagAddr       = flag.String("addr", "127.0.0.1:18930", "listen address (must be loopback)")
	flagMode       = flag.String("mode", "repeat", "repeat | whitespace | stall | normal")
	flagTPS        = flag.Float64("tps", 173, "output tokens per second (1 token ~ 4 bytes of arguments)")
	flagCap        = flag.Int("cap", 128000, "output token cap; at the cap the stream ends with finish_reason=length / response.incomplete")
	flagStallAfter = flag.Int("stall-after", 2048, "stall mode: argument bytes to send before going silent")
	flagLog        = flag.String("log", "", "JSONL request log path")
	flagDegen      = flag.Int("degen-count", 1, "how many tool-bearing requests get the degenerate reply before switching to normal")
	flagChunk      = flag.Int("chunk", 4, "argument bytes per delta event (~1 token)")
	flagDumpFirst  = flag.String("dump-first", "", "write the first request body here (no headers)")
	flagDumpDir    = flag.String("dump-dir", "", "if set, write every POST body to <dir>/req-<seq>.json (no headers)")
	flagCtx        = flag.Int("ctx", 0, "if >0, /api/v1/models reports this loaded context length for model \"mock\" (LM Studio shape)")
	flagFullOutput = flag.Bool("resp-full-output", true, "responses: include the function_call item with full arguments in the terminal event")
)

// The incident block (1.0.28 transcript v2-go-rename-api-r2.md line 158),
// reproduced as two alternating periods. Tabs are literal, "\n" is a literal
// backslash-n as it appeared inside the streamed arguments, and U+FFFC is the
// object-replacement character the model emitted.
const (
	repeatPrefix = `{"tool":"edit_files","arg.include":"**/*.go","arg.path":"main.go","arg.old":"Foo(","arg.new":"Bar(","`
	repeatA      = "} \uFFFC RENDERING_ARGS####json 1/1 (1)####{\"\t\t:\t\" } HIDDEN 1/1 (1)####.json 1/1 (1)####\\n\\nchannel: commentary \uFFFC to=functions.invoke_tool \uFFFCcommentary \uFFFCjson\\n{\"\t\t,\t\" "
	repeatB      = "} \uFFFC RENDERING_ARGS####json 1/1 (1)####{\"\t\t:\t\" } \uFFFC RENDERING_ARGS####json 1/1 (1)####{\"\t\t,\t\" "
	wsPrefix     = `{"tool":"edit_files","arg.path":"main.go","arg.old":"Foo(","arg.new":"`
)

type logRec struct {
	Seq                 int             `json:"seq"`
	Method              string          `json:"method"`
	Path                string          `json:"path"`
	Model               json.RawMessage `json:"model,omitempty"`
	Stream              json.RawMessage `json:"stream,omitempty"`
	MaxTokens           json.RawMessage `json:"max_tokens,omitempty"`
	MaxCompletionTokens json.RawMessage `json:"max_completion_tokens,omitempty"`
	MaxOutputTokens     json.RawMessage `json:"max_output_tokens,omitempty"`
	NTools              int             `json:"n_tools"`
	Tool                string          `json:"tool,omitempty"`
	HasRunawayNote      bool            `json:"has_runaway_note"`
	HasTruncationResult bool            `json:"has_truncation_result"`
	BodyBytes           int             `json:"body_bytes"`
	Reply               string          `json:"reply"`
	Start               string          `json:"start"`
	End                 string          `json:"end"`
	DurMS               int64           `json:"dur_ms"`
	BytesSent           int64           `json:"bytes_sent"`
	ArgsBytes           int64           `json:"args_bytes"`
	TokensSent          int64           `json:"tokens_sent"`
	Ended               string          `json:"ended"`
}

var (
	mu        sync.Mutex
	seq       int
	degenUsed int
	logFile   *os.File
)

func writeLog(r *logRec) {
	mu.Lock()
	defer mu.Unlock()
	if logFile == nil {
		return
	}
	b, _ := json.Marshal(r)
	logFile.Write(append(b, '\n'))
}

// gen yields the argument text for a mode, chunked without splitting runes.
type gen struct {
	mode   string
	prefix string
	buf    string
	n      int
}

func (g *gen) next(size int) string {
	for len(g.buf) < size*2 {
		switch g.mode {
		case "whitespace":
			g.buf += strings.Repeat(` \n`, 64)
		default:
			if g.n%3 == 2 {
				g.buf += repeatB
			} else {
				g.buf += repeatA
			}
		}
		g.n++
	}
	cut := size
	for cut < len(g.buf) && !utf8.RuneStart(g.buf[cut]) {
		cut++
	}
	s := g.buf[:cut]
	g.buf = g.buf[cut:]
	return s
}

func newGen(mode string) *gen {
	g := &gen{mode: mode}
	if mode == "whitespace" {
		g.buf = wsPrefix
	} else {
		g.buf = repeatPrefix
	}
	return g
}

type countingWriter struct {
	w http.ResponseWriter
	f http.Flusher
	n int64
}

func (c *countingWriter) send(s string) error {
	n, err := io.WriteString(c.w, s)
	c.n += int64(n)
	return err
}

func toolNames(req map[string]any) []string {
	var names []string
	tools, _ := req["tools"].([]any)
	for _, t := range tools {
		m, _ := t.(map[string]any)
		if m == nil {
			continue
		}
		if fn, ok := m["function"].(map[string]any); ok {
			if n, _ := fn["name"].(string); n != "" {
				names = append(names, n)
				continue
			}
		}
		if n, _ := m["name"].(string); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func pickTool(names []string) string {
	for _, n := range names {
		if n == "invoke_tool" {
			return n
		}
	}
	for _, n := range []string{"edit_files", "edit", "write_file"} {
		for _, m := range names {
			if m == n {
				return m
			}
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

func handler(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		raw := map[string]json.RawMessage{}
		_ = json.Unmarshal(body, &raw)

		mu.Lock()
		seq++
		rec := &logRec{Seq: seq, Method: r.Method, Path: r.URL.Path, BodyBytes: len(body), Start: start.Format(time.RFC3339Nano)}
		if seq == 1 && *flagDumpFirst != "" {
			_ = os.WriteFile(*flagDumpFirst, body, 0o600)
		}
		if *flagDumpDir != "" {
			_ = os.WriteFile(fmt.Sprintf("%s/req-%d.json", *flagDumpDir, seq), body, 0o600)
		}
		names := toolNames(req)
		degen := false
		if len(names) > 0 && degenUsed < *flagDegen && *flagMode != "normal" {
			degenUsed++
			degen = true
		}
		mu.Unlock()

		rec.Model, rec.Stream = raw["model"], raw["stream"]
		rec.MaxTokens, rec.MaxCompletionTokens, rec.MaxOutputTokens = raw["max_tokens"], raw["max_completion_tokens"], raw["max_output_tokens"]
		rec.NTools = len(names)
		rec.HasRunawayNote = bytes.Contains(body, []byte("[runaway tool call]"))
		rec.HasTruncationResult = bytes.Contains(body, []byte("output token limit"))
		tool := pickTool(names)
		rec.Tool = tool

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		cw := &countingWriter{w: w, f: f}
		if f != nil {
			f.Flush()
		}
		if degen {
			rec.Reply = *flagMode
			rec.Ended, rec.ArgsBytes, rec.TokensSent = streamDegen(r, cw, kind, tool)
		} else {
			rec.Reply = "done"
			rec.Ended = streamDone(cw, kind)
		}
		end := time.Now()
		rec.End = end.Format(time.RFC3339Nano)
		rec.DurMS = end.Sub(start).Milliseconds()
		rec.BytesSent = cw.n
		writeLog(rec)
	}
}

func sseChat(cw *countingWriter, v any) error {
	b, _ := json.Marshal(v)
	return cw.send("data: " + string(b) + "\n\n")
}

func sseResp(cw *countingWriter, typ string, v map[string]any) error {
	v["type"] = typ
	b, _ := json.Marshal(v)
	return cw.send("event: " + typ + "\ndata: " + string(b) + "\n\n")
}

func flush(cw *countingWriter) {
	if cw.f != nil {
		cw.f.Flush()
	}
}

func usageChat(out int) map[string]any {
	return map[string]any{"prompt_tokens": 1000, "completion_tokens": out, "total_tokens": 1000 + out}
}

func usageResp(out int) map[string]any {
	return map[string]any{"input_tokens": 1000, "output_tokens": out, "total_tokens": 1000 + out}
}

func streamDone(cw *countingWriter, kind string) string {
	if kind == "chat" {
		sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": nil}}})
		sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
		sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{}, "usage": usageChat(1)})
		cw.send("data: [DONE]\n\n")
		flush(cw)
		return "done"
	}
	sseResp(cw, "response.created", map[string]any{"response": map[string]any{"id": "resp_d", "status": "in_progress", "output": []any{}}})
	sseResp(cw, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}}})
	sseResp(cw, "response.output_text.delta", map[string]any{"item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": "done"})
	msg := map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "done", "annotations": []any{}}}}
	sseResp(cw, "response.output_item.done", map[string]any{"output_index": 0, "item": msg})
	sseResp(cw, "response.completed", map[string]any{"response": map[string]any{"id": "resp_d", "status": "completed", "output": []any{msg}, "usage": usageResp(1)}})
	flush(cw)
	return "done"
}

// streamDegen streams the degenerate tool call at -tps until the cap, a stall,
// or the client going away. Returns (ended, argsBytes, tokens).
func streamDegen(r *http.Request, cw *countingWriter, kind, tool string) (string, int64, int64) {
	ctx := r.Context()
	if kind == "chat" {
		if sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": tool, "arguments": ""}}}}, "finish_reason": nil}}}) != nil {
			return "write_error", 0, 0
		}
	} else {
		sseResp(cw, "response.created", map[string]any{"response": map[string]any{"id": "resp_1", "status": "in_progress", "output": []any{}}})
		if sseResp(cw, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": tool, "arguments": "", "status": "in_progress"}}) != nil {
			return "write_error", 0, 0
		}
	}
	flush(cw)

	g := newGen(*flagMode)
	var args strings.Builder
	var argBytes int64
	capBytes := int64(*flagCap) * 4
	bps := *flagTPS * 4
	start := time.Now()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if *flagMode == "stall" && argBytes >= int64(*flagStallAfter) {
			flush(cw)
			<-ctx.Done()
			return "client_closed_during_stall", argBytes, argBytes / 4
		}
		target := int64(time.Since(start).Seconds() * bps)
		if target > capBytes {
			target = capBytes
		}
		if *flagMode == "stall" && target > int64(*flagStallAfter) {
			target = int64(*flagStallAfter)
		}
		for argBytes < target {
			d := g.next(*flagChunk)
			argBytes += int64(len(d))
			if *flagFullOutput {
				args.WriteString(d)
			}
			var err error
			if kind == "chat" {
				err = sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": d}}}}, "finish_reason": nil}}})
			} else {
				err = sseResp(cw, "response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "output_index": 0, "delta": d})
			}
			if err != nil {
				return "client_closed", argBytes, argBytes / 4
			}
		}
		flush(cw)
		if argBytes >= capBytes {
			toks := int(argBytes / 4)
			if kind == "chat" {
				sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "length"}}})
				sseChat(cw, map[string]any{"id": "c", "object": "chat.completion.chunk", "model": "mock", "choices": []any{}, "usage": usageChat(toks)})
				cw.send("data: [DONE]\n\n")
			} else {
				out := []any{}
				if *flagFullOutput {
					out = append(out, map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": tool, "arguments": args.String(), "status": "incomplete"})
				}
				sseResp(cw, "response.incomplete", map[string]any{"response": map[string]any{"id": "resp_1", "status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "output": out, "usage": usageResp(toks)}})
			}
			flush(cw)
			return "cap", argBytes, int64(toks)
		}
		select {
		case <-ctx.Done():
			return "client_closed", argBytes, argBytes / 4
		case <-tick.C:
		}
	}
}

func main() {
	flag.Parse()
	host, _, err := net.SplitHostPort(*flagAddr)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		log.Fatalf("refusing non-loopback address %q", *flagAddr)
	}
	if *flagLog != "" {
		logFile, err = os.OpenFile(*flagLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			log.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handler("chat"))
	mux.HandleFunc("/v1/responses", handler("responses"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seq++
		rec := &logRec{Seq: seq, Method: r.Method, Path: r.URL.Path, Reply: "other", Start: time.Now().Format(time.RFC3339Nano)}
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			if *flagCtx > 0 && strings.HasPrefix(r.URL.Path, "/api/") {
				fmt.Fprintf(w, `{"object":"list","data":[{"id":"mock","object":"model","type":"llm","max_context_length":%d,"loaded_context_length":%d}]}`, *flagCtx, *flagCtx)
			} else {
				fmt.Fprint(w, `{"object":"list","data":[{"id":"mock","object":"model","owned_by":"mock"}]}`)
			}
			rec.Ended = "models"
		} else {
			http.NotFound(w, r)
			rec.Ended = "404"
		}
		rec.End = time.Now().Format(time.RFC3339Nano)
		writeLog(rec)
	})
	ln, err := net.Listen("tcp", *flagAddr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("mockllm listening on %s mode=%s tps=%g cap=%d\n", ln.Addr(), *flagMode, *flagTPS, *flagCap)
	log.Fatal(http.Serve(ln, mux))
}
