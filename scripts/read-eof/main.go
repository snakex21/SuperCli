// Controlled continuation after reading a fixed range of a synthetic file.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

type measured struct {
	llm.Provider
	Calls int
}

func (p *measured) Complete(ctx context.Context, m []llm.Message, t []llm.ToolDef) (<-chan llm.Delta, error) {
	p.Calls++
	return p.Provider.Complete(ctx, m, t)
}

type outcome struct {
	Case, Answer, Error, InitialError string
	Correct                           bool
	ModelCalls, InitialBytes          int
	Milliseconds                      int64
	Usage                             agent.Usage
	Tools                             []agent.ToolCallEvent
}

func main() {
	if len(os.Args) != 3 {
		panic("model absolute-output.json")
	}
	model, out := os.Args[1], os.Args[2]
	if !filepath.IsAbs(out) {
		panic("absolute output required")
	}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	must(llm.SetReasoningEffort("low"))
	var backend llm.Provider
	var err error
	switch model {
	case "muse-spark-1.3-contributor-free":
		backend, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, Timeout: 90 * time.Second})
	case "qwen3.8-27b-uncensored":
		backend, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, MaxTokens: 2048, Timeout: 90 * time.Second})
	default:
		panic("unsupported model")
	}
	must(err)
	root := filepath.Join(filepath.Dir(out), "repo-"+strings.TrimSuffix(filepath.Base(out), ".json"))
	var results []outcome
	for _, name := range []string{"exact-end", "more-lines-control"} {
		r := evaluate(filepath.Join(root, name), name, backend, caps)
		results = append(results, r)
		data, _ := json.MarshalIndent(map[string]any{"model": model, "fixed_initial_tool": true, "cases": results}, "", "  ")
		must(os.WriteFile(out, data, 0600))
		fmt.Printf("%s correct=%v calls=%d tools=%d ms=%d input=%d initialError=%t error=%s\n", name, r.Correct, r.ModelCalls, len(r.Tools), r.Milliseconds, r.Usage.Input, r.InitialError != "", r.Error)
		if r.Error != "" {
			break
		}
	}
}
func evaluate(root, name string, backend llm.Provider, caps *llm.CapabilityRegistry) outcome {
	must(os.MkdirAll(root, 0700))
	var body strings.Builder
	for i := 1; i < 20; i++ {
		fmt.Fprintf(&body, "worker%02d=enabled\n", i)
	}
	body.WriteString("RetryLimit=6842\n")
	wantLines := 20
	if name == "more-lines-control" {
		for i := 21; i <= 25; i++ {
			fmt.Fprintf(&body, "worker%02d=enabled\n", i)
		}
		wantLines = 25
	}
	must(os.WriteFile(filepath.Join(root, "limits.txt"), []byte(body.String()), 0600))
	reg := tools.NewRegistry()
	spec := tools.NewReadLines(root).Spec()
	reg.MustRegister(spec)
	reg.MarkAlwaysOn(spec.Name)
	raw, _ := json.Marshal(map[string]any{"file": "limits.txt", "from": 1, "to": 20})
	result, err := spec.Fn(context.Background(), raw)
	must(err)
	view := reg.ModelResultContent(spec.Name, result)
	p := &measured{Provider: backend}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: p, Caps: caps, Registry: reg, BaseDir: root, MaxSteps: 6, System: "Inspect files accurately and answer concisely."})
	must(err)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleUser, Content: "Inspect limits.txt and determine the total number of entries and RetryLimit."},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "initial-read", Name: spec.Name, Arguments: string(raw)}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "initial-read", Name: spec.Name, Content: view})
	r := outcome{Case: name, InitialBytes: len(view)}
	if result.Err != nil {
		r.InitialError = result.Err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "read-eof-"+filepath.Base(filepath.Dir(root))+"-"+name)
	start := time.Now()
	events, err := loop.Run(ctx, "Sprawdź cały limits.txt: ile łącznie ma wpisów i jaka jest wartość RetryLimit? Odpowiedz jedną linią: entries=<liczba> RetryLimit=<wartość>.")
	if err != nil {
		r.Error = err.Error()
	} else {
		for event := range events {
			switch e := event.(type) {
			case agent.MessageEvent:
				r.Answer += e.Text
			case agent.ToolCallEvent:
				r.Tools = append(r.Tools, e)
			case agent.DoneEvent:
				r.Usage = e.Usage
			case agent.ErrorEvent:
				r.Error = e.Err.Error()
				r.Usage = e.Usage
			}
		}
	}
	r.ModelCalls = p.Calls
	r.Milliseconds = time.Since(start).Milliseconds()
	r.Correct = r.Error == "" && strings.Contains(r.Answer, fmt.Sprintf("entries=%d", wantLines)) && strings.Contains(r.Answer, "RetryLimit=6842")
	return r
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
