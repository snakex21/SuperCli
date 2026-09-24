// Read-only handoff experiment. Only synthetic files under the output directory
// are changed by this harness; models have no shell or mutation tools.
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
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

type counter struct {
	llm.Provider
	calls atomic.Int64
}

func (p *counter) Complete(ctx context.Context, m []llm.Message, t []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	return p.Provider.Complete(ctx, m, t)
}

type trace struct {
	Phase, Name string
	Args        json.RawMessage
}

func main() {
	if len(os.Args) != 3 {
		panic("model absolute-output.json")
	}
	model, outfile := os.Args[1], os.Args[2]
	if model != "qwen3.8-27b-uncensored" && model != "muse-spark-1.3-contributor-free" {
		panic("unsupported model")
	}
	if !filepath.IsAbs(outfile) {
		panic("absolute output path required")
	}
	// A per-run fixture avoids concurrent models changing one another's files.
	root := filepath.Join(filepath.Dir(outfile), "repo-"+strings.TrimSuffix(filepath.Base(outfile), ".json"))
	for _, dir := range []string{"src", "config"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			panic(err)
		}
	}
	write := func(attempts, delay int) {
		if err := os.WriteFile(filepath.Join(root, "src/retry.go"), []byte(fmt.Sprintf("package retry\n\nconst MaxAttempts = %d\n", attempts)), 0600); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(root, "config/runtime.env"), []byte(fmt.Sprintf("RETRY_DELAY_MS=%d\n", delay)), 0600); err != nil {
			panic(err)
		}
	}
	write(7, 235)
	result := map[string]any{"model": model, "fixture": root}
	defer func() {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(outfile, b, 0600); err != nil {
			panic(err)
		}
		fmt.Println(string(b))
	}()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	_ = llm.SetReasoningEffort("low")
	var backend llm.Provider
	var err error
	if strings.HasSuffix(model, "-free") {
		backend, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, Timeout: 120 * time.Second})
	} else {
		backend, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, MaxTokens: 2048, Timeout: 120 * time.Second})
	}
	if err != nil {
		result["error"] = err.Error()
		return
	}
	provider := &counter{Provider: backend}
	reg := tools.NewRegistry()
	phase := "worker-inspection"
	var mu sync.Mutex
	calls := []trace{}
	for _, spec := range []tools.Tool{tools.NewReadLines(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewSearchCode(root).Spec(), tools.NewListDir(root).Spec()} {
		fn, name := spec.Fn, spec.Name
		spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			mu.Lock()
			calls = append(calls, trace{Phase: phase, Name: name, Args: append(json.RawMessage(nil), args...)})
			mu.Unlock()
			return fn(ctx, args)
		}
		reg.MustRegister(spec)
		reg.MarkAlwaysOn(spec.Name)
	}
	parent, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: root, MaxSteps: 8, System: "Answer the user's question using available evidence."})
	if err != nil {
		result["error"] = err.Error()
		return
	}
	sub := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(sub, agent.BuiltinSubAgents())
	task, err := agent.NewAgentTool(sub, parent, reg, provider, caps, agent.NewLoop)
	if err != nil {
		result["error"] = err.Error()
		return
	}
	task.MaxSteps = 8
	task.TimeoutPerStep = 120 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "worker-handoff-"+filepath.Base(outfile))
	prompt := "Przeczytaj src/retry.go i config/runtime.env. W raporcie zwróć tylko ścieżki przeczytanych plików, bez wartości konfiguracji. To przegląd plików przed zmianą."
	raw, _ := json.Marshal(map[string]string{"agent": "explore", "prompt": prompt})
	start := time.Now()
	res, err := task.Spec().Fn(ctx, raw)
	result["initial_worker_ms"] = time.Since(start).Milliseconds()
	result["worker_report"] = res.Text
	if err != nil {
		result["error"] = err.Error()
		return
	}
	if res.Err != nil {
		result["error"] = res.Err.Error()
		return
	}
	worker, ok := task.Workers.Get("worker-1")
	if !ok {
		result["error"] = "worker missing"
		return
	}
	before := worker.Snapshot()
	result["initial_worker"] = before
	visible := reg.ModelResultContent("task", res)
	result["handoff_bytes"] = len(visible)
	result["attachment_bytes"] = len(res.RetainedText)
	result["handoff"] = visible
	// Deliberate controlled change: the later question asks about the earlier
	// observation, which is accessible in both variants through send_message.
	write(99, 9001)
	send := agent.NewSendMessageTool(task.Workers).Spec()
	reg.MustRegister(send)
	reg.MarkAlwaysOn(send.Name)
	parent.Messages = append(parent.Messages,
		llm.Message{Role: llm.RoleUser, Content: prompt},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "inspection", Name: "task", Arguments: string(raw)}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "inspection", Name: "task", Content: visible},
		llm.Message{Role: llm.RoleAssistant, Content: "Przegląd workera zakończony."},
	)
	phase = "handoff-followup"
	beforeCalls := provider.calls.Load()
	start = time.Now()
	events, err := parent.Run(ctx, "Pliki zostały już zmienione po zakończeniu worker-1. Podaj dokładne wartości MaxAttempts i RETRY_DELAY_MS z jego wcześniejszego odczytu, ze ścieżkami plików. Potrzebuję stanu sprzed zmiany.")
	if err != nil {
		result["error"] = err.Error()
		return
	}
	var answer strings.Builder
	parentCalls := []agent.ToolCallEvent{}
	for event := range events {
		switch e := event.(type) {
		case agent.MessageEvent:
			answer.WriteString(e.Text)
		case agent.ToolCallEvent:
			parentCalls = append(parentCalls, e)
		case agent.DoneEvent:
			result["parent_steps"] = e.Steps
			result["parent_usage"] = e.Usage
		case agent.ErrorEvent:
			result["error"] = e.Err.Error()
			result["parent_steps"] = e.Steps
			result["parent_usage"] = e.Usage
		}
	}
	text := answer.String()
	result["followup_ms"] = time.Since(start).Milliseconds()
	result["answer"] = text
	result["correct"] = regexp.MustCompile("\\b7\\b").MatchString(text) && strings.Contains(text, "235") && strings.Contains(text, "src/retry.go") && strings.Contains(text, "config/runtime.env")
	result["followup_model_calls"] = provider.calls.Load() - beforeCalls
	after := worker.Snapshot()
	result["worker_followup_steps"] = after.Steps - before.Steps
	result["worker_followup_input"] = after.TokensIn - before.TokensIn
	result["worker_followup_output"] = after.TokensOut - before.TokensOut
	result["parent_tool_calls"] = parentCalls
	result["source_reads"] = calls
	result["parent_history"] = parent.Messages
}
