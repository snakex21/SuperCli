// Controlled continuation after a history search over synthetic saved evidence.
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
	"supercli/internal/storage/session"
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
	for _, name := range []string{"bare-file", "quoted-control"} {
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
	store, err := session.OpenStore(filepath.Join(root, "data"))
	must(err)
	defer store.Close()
	sess, err := store.Create(root, "fixture", "historical policy")
	must(err)
	writer := session.NewWriter(store, sess.ID)
	args, _ := json.Marshal(map[string]any{"file": "src/retry-policy.go", "from": 1, "to": 2})
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "Read the previous retry policy."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old-read", Name: "read_context", Arguments: string(args)}}},
		{Role: llm.RoleTool, ToolCallID: "old-read", Name: "read_context", Content: "src/retry-policy.go\nBudgetLimit=6842\n"},
		{Role: llm.RoleAssistant, Content: "Previous policy inspected."},
	} {
		must(writer.AppendMessage(context.Background(), m))
	}
	reg := tools.NewRegistry()
	spec := tools.NewSearchHistory(store).Spec()
	reg.MustRegister(spec)
	reg.MarkAlwaysOn(spec.Name)
	query := "BudgetLimit src/retry-policy.go"
	if name == "quoted-control" {
		query = "BudgetLimit AND \"src/retry-policy.go\""
	}
	raw, _ := json.Marshal(map[string]any{"query": query, "session_id": sess.ID, "role": "tool"})
	result, err := spec.Fn(context.Background(), raw)
	must(err)
	view := reg.ModelResultContent(spec.Name, result)
	p := &measured{Provider: backend}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: p, Caps: caps, Registry: reg, BaseDir: root, MaxSteps: 6, System: "Answer accurately using saved conversation evidence."})
	must(err)
	loop.Messages = append(loop.Messages,
		llm.Message{Role: llm.RoleUser, Content: "Find the historical BudgetLimit for src/retry-policy.go."},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "history", Name: spec.Name, Arguments: string(raw)}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "history", Name: spec.Name, Content: view})
	r := outcome{Case: name, InitialBytes: len(view)}
	if result.Err != nil {
		r.InitialError = result.Err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "history-terms-"+filepath.Base(filepath.Dir(root))+"-"+name)
	start := time.Now()
	events, err := loop.Run(ctx, "Podaj historyczną wartość BudgetLimit w src/retry-policy.go. Odpowiedz krótko: ścieżka i wartość.")
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
	r.Correct = r.Error == "" && regexp.MustCompile("\\b6842\\b").MatchString(r.Answer) && strings.Contains(r.Answer, "src/retry-policy.go")
	return r
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
