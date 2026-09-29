// Controlled continuation after a search capped at one match.
// Models can only read synthetic files and stored output in the experiment folder.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
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
	if len(os.Args) != 4 {
		panic("free-model absolute-output.json absolute-zen-catalog.json")
	}
	model, outfile, catalog := os.Args[1], os.Args[2], os.Args[3]
	if !strings.HasSuffix(model, "-free") || !filepath.IsAbs(outfile) || !filepath.IsAbs(catalog) {
		panic("free model and absolute paths required")
	}
	root := filepath.Join(filepath.Dir(outfile), "repo-"+strings.TrimSuffix(filepath.Base(outfile), ".json"))
	dataDir := filepath.Join(root, "data")
	must(os.MkdirAll(dataDir, 0700))
	publicCatalog, err := os.ReadFile(catalog)
	must(err)
	must(os.WriteFile(filepath.Join(dataDir, "opencode_zen_catalog.json"), publicCatalog, 0600))
	cfg, err := config.Load(config.FlagOverrides{Provider: config.ProviderOpenAI, BaseURL: "https://opencode.ai/zen/v1", Model: model})
	must(err)
	// Free public endpoints do not need the user's API key or saved configuration.
	cfg.APIKey = ""
	cfg.MaxTokens, cfg.Timeout, cfg.ConnectTimeout = 2048, 60*time.Second, 10*time.Second
	must(llm.SetReasoningEffort("low"))
	var results []outcome
	for _, scenario := range []string{"locations", "automatic"} {
		caps := llm.NewCapabilityRegistry()
		backend, err := factory.New(nil, dataDir, caps).Build(cfg, "eval-search-limit")
		must(err)
		r := evaluate(root, scenario, backend, caps)
		results = append(results, r)
		data, err := json.MarshalIndent(map[string]any{"model": model, "fixed_initial_tool": true, "cases": results}, "", "  ")
		must(err)
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
	must(os.WriteFile(filepath.Join(root, "retry.go"), []byte("package retry\nfunc RetryDelay() int {\n return 713\n}\n\n\n\n\n\n\nfunc RetryDelayFromEnv() int { return 99 }\n"), 0600))
	registry := tools.NewRegistry()
	for _, spec := range []tools.Tool{tools.NewReadLines(root).Spec(), tools.NewSearchCode(root).Spec()} {
		registry.MustRegister(spec)
		registry.MarkAlwaysOn(spec.Name)
	}
	provider := &measured{Provider: backend}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Caps: caps, Registry: registry, BaseDir: root, MaxSteps: 4, System: "Answer accurately using available file evidence. Do not modify files."})
	must(err)
	searchArgs := map[string]any{"query": "func RetryDelay", "max": 1, "include": "*.go"}
	if scenario == "locations" {
		searchArgs["context"] = 0
	}
	args, _ := json.Marshal(searchArgs)
	spec, _ := registry.Get("search_code")
	read, err := spec.Fn(context.Background(), args)
	must(err)
	must(read.Err)
	view := registry.ModelResultContent("search_code", read)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleUser, Content: "Find the RetryDelay function."},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "initial_read", Name: "search_code", Arguments: string(args)}}},
		llm.Message{Role: llm.RoleTool, Name: "search_code", ToolCallID: "initial_read", Content: view})
	r := outcome{Case: scenario, FullReadBytes: len(read.Text), ModelReadBytes: len(view)}
	if read.RetainedText != "" {
		r.FullReadBytes = len(read.RetainedText)
	}
	prompt := "What integer does RetryDelay() return? Answer briefly with its file path and value."
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "search-limit-context-"+filepath.Base(base)+"-"+scenario)
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
	expected := "713"
	r.Correct = false
	valuePattern := regexp.MustCompile("(^|[^0-9])" + expected + "([^0-9]|$)")
	for _, line := range strings.Split(r.Answer, "\n") {
		if r.Error == "" && strings.Contains(line, "retry.go") && valuePattern.MatchString(line) {
			r.Correct = true
		}
	}
	return r
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
