package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type capture struct {
	counts []int
	dir    string
}
type capturedBody struct {
	io.Reader
	io.Closer
	b    *bytes.Buffer
	path string
}

func (b *capturedBody) Close() error {
	e := b.Closer.Close()
	_ = os.WriteFile(b.path, b.b.Bytes(), 0600)
	return e
}
func (c *capture) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	n := 0
	for _, key := range []string{"input", "messages"} {
		var rows []map[string]json.RawMessage
		_ = json.Unmarshal(body[key], &rows)
		for _, row := range rows {
			var kind string
			_ = json.Unmarshal(row["type"], &kind)
			if kind == "reasoning" {
				n++
			}
			if len(row["reasoning_content"]) > 2 {
				n++
			}
		}
	}
	c.counts = append(c.counts, n)
	_ = os.WriteFile(filepath.Join(c.dir, fmt.Sprintf("request-%d.json", len(c.counts))), raw, 0600)
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	b := &bytes.Buffer{}
	res.Body = &capturedBody{Reader: io.TeeReader(res.Body, b), Closer: res.Body, b: b, path: filepath.Join(c.dir, fmt.Sprintf("response-%d.sse", len(c.counts)))}
	return res, nil
}
func main() {
	if len(os.Args) != 3 {
		panic("model output-directory")
	}
	model, dir := os.Args[1], os.Args[2]
	if model != "qwen3.8-27b-uncensored" && model != "muse-spark-1.3-contributor-free" {
		panic("fixture permits only the two explicitly selected models")
	}
	if !filepath.IsAbs(dir) {
		panic("absolute fixture output directory required")
	}
	_ = os.MkdirAll(dir, 0700)
	result := map[string]any{"model": model}
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(result) }()
	cap := &capture{dir: dir}
	client := &http.Client{Transport: cap}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	_ = llm.SetReasoningEffort("low")
	var p llm.Provider
	var err error
	if strings.HasSuffix(model, "-free") {
		p, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: model, HTTPClient: client, Capabilities: caps, Timeout: 120 * time.Second})
	} else {
		p, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: model, HTTPClient: client, Capabilities: caps, MaxTokens: 3072, Timeout: 120 * time.Second})
	}
	if err != nil {
		result["error"] = err.Error()
		return
	}
	store, err := session.OpenStore(dir)
	if err != nil {
		result["error"] = err.Error()
		return
	}
	defer store.Close()
	sess, err := store.Create(dir, p.Name(), "")
	if err != nil {
		result["error"] = err.Error()
		return
	}
	writer := session.NewWriter(store, sess.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 280*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "native-loop-"+sess.ID)
	prompts := []string{
		"Synthetic JavaScript fixture: splitEscaped(s) splits comma-separated fields, a backslash escapes the next character, including commas and backslashes; preserve empty fields and a literal dangling backslash. In at most 40 words explain the needed state. Do not write code yet. Everything needed is in this message.",
		"Now implement splitEscaped(s). Reply with the function source only. No tools are needed.",
	}
	texts := []string{}
	times := []int64{}
	nativeSaved := []int{}
	for _, prompt := range prompts {
		history, e := store.ReadModelContext(ctx, sess.ID)
		if e != nil {
			result["error"] = e.Error()
			return
		}
		r := tools.NewRegistry()
		r.MustRegister(tools.Tool{Name: "read_lines", Description: "Read synthetic fixture; no files or commands are available.", Schema: "{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\"}}}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "All fixture requirements are in the user message."}, nil
		}})
		l, e := agent.NewLoop(agent.LoopConfig{Provider: p, Registry: r, Writer: writer, InitialMessages: history, MaxSteps: 4, System: "You are repairing a synthetic JavaScript fixture. Use only the supplied requirements."})
		if e != nil {
			result["error"] = e.Error()
			return
		}
		start := time.Now()
		ch, e := l.Run(ctx, prompt)
		if e != nil {
			result["error"] = e.Error()
			return
		}
		var text strings.Builder
		for ev := range ch {
			switch e := ev.(type) {
			case agent.MessageEvent:
				text.WriteString(e.Text)
			case agent.ErrorEvent:
				result["error"] = fmt.Sprint(e.Err)
			}
		}
		texts = append(texts, text.String())
		times = append(times, time.Since(start).Milliseconds())
		rows, e := store.ReadModelContext(ctx, sess.ID)
		if e != nil {
			result["error"] = e.Error()
			return
		}
		n := 0
		for _, m := range rows {
			for _, part := range m.Parts {
				if part.Type == llm.PartTypeReasoning {
					n++
				}
			}
		}
		nativeSaved = append(nativeSaved, n)
	}
	result["nativeRequestBlocks"] = cap.counts
	result["nativeSavedBlocks"] = nativeSaved
	result["texts"] = texts
	result["ms"] = times
}
