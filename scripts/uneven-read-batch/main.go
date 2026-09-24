// Controlled continuation after reading an uneven batch of synthetic files.
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
	cases := []string{"middle", "head", "balanced"}
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
	var ranges []string
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("file%d.env", i)
		count := 1
		if i == 5 {
			count = 100
		}
		middle := 55
		if scenario == "balanced" {
			count = 8
			middle = 5
		}
		var body strings.Builder
		for n := 1; n <= count; n++ {
			if i == 5 && n == middle {
				body.WriteString("BudgetLimit=6842\n")
			} else if i == 5 && n == 3 {
				body.WriteString("InitialLimit=1739\n")
			} else if count == 1 {
				body.WriteString("Flag=off\n")
			} else {
				fmt.Fprintf(&body, "line_%03d padding abcdefghijklmnopqrstuvwxyz0123456789\n", n)
			}
		}
		must(os.WriteFile(filepath.Join(root, name), []byte(body.String()), 0600))
		ranges = append(ranges, name+":1-100")
	}
	registry := tools.NewRegistry()
	for _, spec := range []tools.Tool{tools.NewReadMany(root).Spec(), tools.NewReadLines(root).Spec(), tools.NewSearchCode(root).Spec()} {
		registry.MustRegister(spec)
		registry.MarkAlwaysOn(spec.Name)
	}
	provider := &measured{Provider: backend}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Caps: caps, Registry: registry, BaseDir: root, MaxSteps: 6, System: "Answer accurately using available file evidence. Do not modify files."})
	must(err)
	args, _ := json.Marshal(map[string]string{"reads": strings.Join(ranges, " | ")})
	spec, _ := registry.Get("read_many")
	read, err := spec.Fn(context.Background(), args)
	must(err)
	must(read.Err)
	view := registry.ModelResultContent("read_many", read)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleUser, Content: "Odczytaj wszystkie pliki z podanych zakresów."},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "initial_read", Name: "read_many", Arguments: string(args)}}},
		llm.Message{Role: llm.RoleTool, Name: "read_many", ToolCallID: "initial_read", Content: view})
	r := outcome{Case: scenario, FullReadBytes: len(read.Text), ModelReadBytes: len(view)}
	if read.RetainedText != "" {
		r.FullReadBytes = len(read.RetainedText)
	}
	field := "BudgetLimit"
	if scenario == "head" {
		field = "InitialLimit"
	}
	prompt := "Podaj dokładne wartości " + field + " dla file5.env na podstawie odczytanych plików. Odpowiedz krótko: ścieżka pliku i wartość."
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "uneven-batch-"+filepath.Base(base)+"-"+scenario)
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
	expected := "6842"
	if scenario == "head" {
		expected = "1739"
	}
	r.Correct = false
	valuePattern := regexp.MustCompile("(^|[^0-9])" + expected + "([^0-9]|$)")
	for _, line := range strings.Split(r.Answer, "\n") {
		if r.Error == "" && strings.Contains(line, "file5.env") && valuePattern.MatchString(line) {
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
