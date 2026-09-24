package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"supercli/internal/llm"
)

type input struct {
	Base      string
	Model     string
	Transport string
	History   []json.RawMessage
	Session   string
	Effort    string
}
type output struct {
	Text         string
	Reasoning    string
	Calls        []llm.ToolCall
	Usage        *llm.Usage
	Error        string
	TTFTMS       int64
	ElapsedMS    int64
	RequestBytes int
	SSE          string
}
type capture struct {
	in           input
	raw          bytes.Buffer
	requestBytes int
}
type bodyCapture struct {
	io.Reader
	io.Closer
}

func (c *capture) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	var body map[string]json.RawMessage
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	key := "messages"
	if c.in.Transport == "responses" {
		key = "input"
	}
	body[key], err = json.Marshal(c.in.History)
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	c.requestBytes = len(raw)
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(raw))
	clone.ContentLength = int64(len(raw))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	res, err := http.DefaultTransport.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	res.Body = bodyCapture{Reader: io.TeeReader(res.Body, &c.raw), Closer: res.Body}
	return res, nil
}
func main() {
	var in input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		panic(err)
	}
	result := output{}
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(result) }()
	// Evaluation-only: no credentials, no files, and only explicit local or free Zen models.
	if !(in.Base == "http://127.0.0.1:1234/v1" || (in.Base == "https://opencode.ai/zen/v1" && strings.HasSuffix(in.Model, "-free"))) {
		result.Error = "only the configured loopback endpoint or free Zen models allowed"
		return
	}
	if err := llm.SetReasoningEffort(in.Effort); err != nil {
		result.Error = err.Error()
		return
	}
	cap := &capture{in: in}
	client := &http.Client{Transport: cap}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: in.Model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	var provider llm.Provider
	var err error
	switch in.Transport {
	case "responses":
		provider, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: in.Base, Model: in.Model, HTTPClient: client, Capabilities: caps, Timeout: 60 * time.Second})
	case "anthropic":
		provider, err = llm.NewAnthropic(llm.AnthropicConfig{BaseURL: in.Base, Model: in.Model, MaxTokens: 3072, HTTPClient: client, Capabilities: caps, Timeout: 60 * time.Second})
	default:
		provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: in.Base, Model: in.Model, MaxTokens: 3072, HTTPClient: client, Capabilities: caps, Timeout: 60 * time.Second})
	}
	if err != nil {
		result.Error = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, in.Session)
	defs := []llm.ToolDef{
		{Name: "read", Description: "Read a fixture source file.", Schema: `{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`},
		{Name: "bash", Description: "Run fixture checks. No operating-system commands are available.", Schema: `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`},
	}
	start := time.Now()
	stream, err := provider.Complete(ctx, []llm.Message{{Role: llm.RoleSystem, Content: "You are repairing a small JavaScript module. Preserve the requested API. Work only with the supplied fixture."}, {Role: llm.RoleUser, Content: "fixture request"}}, defs)
	if err != nil {
		result.Error = err.Error()
	} else {
		for d := range stream {
			if result.TTFTMS == 0 && (d.Content != "" || d.Reasoning != "" || d.ToolCall != nil) {
				result.TTFTMS = time.Since(start).Milliseconds()
			}
			result.Text += d.Content
			result.Reasoning += d.Reasoning
			if d.ToolCall != nil {
				result.Calls = append(result.Calls, *d.ToolCall)
			}
			if d.Usage != nil {
				result.Usage = d.Usage
			}
			if d.Err != nil {
				result.Error = fmt.Sprint(d.Err)
			}
		}
	}
	result.ElapsedMS = time.Since(start).Milliseconds()
	result.RequestBytes = cap.requestBytes
	result.SSE = cap.raw.String()
}
