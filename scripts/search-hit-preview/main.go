// Controlled continuation after the same real search_code execution.
// Models can only read synthetic files and stored output in the experiment folder.
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
	Bytes []int
}

func (p *measured) Complete(ctx context.Context, m []llm.Message, t []llm.ToolDef) (<-chan llm.Delta, error) {
	b, _ := json.Marshal(m)
	d, _ := json.Marshal(t)
	p.Calls++
	p.Bytes = append(p.Bytes, len(b)+len(d))
	return p.Provider.Complete(ctx, m, t)
}

type outcome struct {
	Case, Answer, Error                       string
	Correct                                   bool
	FullReadBytes, ModelReadBytes, ModelCalls int
	RequestBytes                              []int
	Milliseconds                              int64
	Usage                                     agent.Usage
	Tools                                     []agent.ToolCallEvent
}

func main() {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		panic("model absolute-output.json")
	}
	model, outfile := os.Args[1], os.Args[2]
	if model != "qwen3.8-27b-uncensored" && model != "muse-spark-1.3-contributor-free" {
		panic("unsupported model")
	}
	if !filepath.IsAbs(outfile) {
		panic("absolute output path required")
	}
	root := filepath.Join(filepath.Dir(outfile), "repo-"+strings.TrimSuffix(filepath.Base(outfile), ".json"))
	must(os.MkdirAll(root, 0700))
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	must(llm.SetReasoningEffort("low"))
	var backend llm.Provider
	var err error
	if strings.HasSuffix(model, "-free") {
		backend, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, Timeout: 90 * time.Second})
	} else {
		backend, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, MaxTokens: 2048, Timeout: 90 * time.Second})
	}
	must(err)
	var results []outcome
	cases := []string{"large", "small"}
	if len(os.Args) == 4 {
		cases = strings.Split(os.Args[3], ",")
	}
	for _, scenario := range cases {
		r := evaluate(root, scenario, backend, caps)
		results = append(results, r)
		data, _ := json.MarshalIndent(map[string]any{"model": model, "fixed_initial_tool": true, "cases": results}, "", "  ")
		must(os.WriteFile(outfile, data, 0600))
		fmt.Printf("%s correct=%v calls=%d tools=%d ms=%d input=%d output=%d bytes=%d/%d error=%s\n", scenario, r.Correct, r.ModelCalls, len(r.Tools), r.Milliseconds, r.Usage.Input, r.Usage.Output, r.ModelReadBytes, r.FullReadBytes, r.Error)
		if r.Error != "" {
			break
		}
	}
}
func evaluate(base, scenario string, backend llm.Provider, caps *llm.CapabilityRegistry) outcome {
	root := filepath.Join(base, scenario)
	must(os.MkdirAll(root, 0700))
	names := []string{"alpha", "middle", "omega"}
	values := []string{"1111", "7321", "9999"}
	for i, name := range names {
		body := "BudgetLimit=" + values[i]
		if scenario == "large" && i != 1 {
			body = strings.Repeat("x", 6000) + " " + body + " " + strings.Repeat("z", 6000)
		}
		must(os.WriteFile(filepath.Join(root, name+".env"), []byte(body+"\n"), 0600))
	}
	registry := tools.NewRegistry()
	for _, spec := range []tools.Tool{tools.NewReadMany(root).Spec(), tools.NewReadLines(root).Spec(), tools.NewSearchCode(root).Spec()} {
		registry.MustRegister(spec)
		registry.MarkAlwaysOn(spec.Name)
	}
	provider := &measured{Provider: backend}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Caps: caps, Registry: registry, BaseDir: root, MaxSteps: 6, System: "Answer accurately using available file evidence. Do not modify files."})
	must(err)
	args, _ := json.Marshal(map[string]any{"query": "BudgetLimit", "context": 0})
	spec, _ := registry.Get("search_code")
	read, err := spec.Fn(context.Background(), args)
	must(err)
	must(read.Err)
	view := registry.ModelResultContent("search_code", read)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleUser, Content: "Wyszukaj BudgetLimit w plikach projektu."},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "initial_read", Name: "search_code", Arguments: string(args)}}},
		llm.Message{Role: llm.RoleTool, Name: "search_code", ToolCallID: "initial_read", Content: view})
	r := outcome{Case: scenario, FullReadBytes: len(read.Text), ModelReadBytes: len(view)}
	prompt := "Podaj dokładne wartości BudgetLimit dla alpha.env, middle.env i omega.env na podstawie wyników wyszukiwania. Odpowiedz krótko: po jednej wartości ze ścieżką pliku."
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "search-hit-preview-"+filepath.Base(base)+"-"+scenario)
	start := time.Now()
	events, err := loop.Run(ctx, prompt)
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
	r.Milliseconds = time.Since(start).Milliseconds()
	r.ModelCalls = provider.Calls
	r.RequestBytes = provider.Bytes
	r.Correct = r.Error == ""
	expected := values
	for i, name := range names {
		pattern := "(?m)^.*" + regexp.QuoteMeta(name+".env") + "[^\r\n]*\\b" + expected[i] + "\\b"
		r.Correct = r.Correct && regexp.MustCompile(pattern).MatchString(r.Answer)
	}
	return r
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
