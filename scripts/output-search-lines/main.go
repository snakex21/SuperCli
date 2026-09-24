// Controlled continuation after searching synthetic retained tool output.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	for _, name := range []string{"long-hit-line", "short-line-control", "minified-control"} {
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
	line := "RetryPolicy " + strings.Repeat("label ", 38) + "effective_limit=6842\n"
	if name == "short-line-control" {
		line = "RetryPolicy effective_limit=6842\n"
	}
	source := strings.Repeat("routine log detail\n", 1000) + line + strings.Repeat("routine log detail\n", 1000)
	if name == "minified-control" {
		source = strings.Repeat("z", 20000) + "RetryPolicy effective_limit=6842 " + strings.Repeat("z", 20000)
	}
	reg := tools.NewRegistry()
	reg.EnsureReadOutput()
	preview := reg.CompactModelOutput("ctx_execute", source)
	match := regexp.MustCompile("handle=(out_[a-f0-9]+)").FindStringSubmatch(preview)
	if len(match) != 2 {
		panic("no stored output handle")
	}
	raw, _ := json.Marshal(map[string]any{"handle": match[1], "query": "RetryPolicy"})
	result, err := reg.Execute(context.Background(), "read_output", raw)
	must(err)
	view := reg.ModelResultContent("read_output", result)
	p := &measured{Provider: backend}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: p, Caps: caps, Registry: reg, BaseDir: root, MaxSteps: 6, System: "Inspect files accurately and answer concisely."})
	must(err)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleUser, Content: "Find the effective_limit for RetryPolicy in the stored tool output."},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "initial-read", Name: "read_output", Arguments: string(raw)}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "initial-read", Name: "read_output", Content: view})
	r := outcome{Case: name, InitialBytes: len(view)}
	if result.Err != nil {
		r.InitialError = result.Err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "search-lines-"+filepath.Base(filepath.Dir(root))+"-"+name)
	start := time.Now()
	events, err := loop.Run(ctx, "Jaka jest wartość effective_limit dla RetryPolicy w zapisanym wyniku? Odpowiedz jedną linią: effective_limit=<wartość>.")
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
	r.Correct = r.Error == "" && strings.TrimSpace(r.Answer) == "effective_limit=6842"
	return r
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
