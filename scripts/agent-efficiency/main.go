// Read-only whole-turn comparison on synthetic files. All state stays in the output folder.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type request struct {
	Bytes       int
	ToolResults int
	Tools       []string
}
type measured struct {
	llm.Provider
	mu       sync.Mutex
	Requests []request
}

func (p *measured) Complete(ctx context.Context, m []llm.Message, t []llm.ToolDef) (<-chan llm.Delta, error) {
	b, _ := json.Marshal(m)
	d, _ := json.Marshal(t)
	r := request{Bytes: len(b) + len(d)}
	for _, v := range m {
		if v.Role == llm.RoleTool {
			r.ToolResults++
		}
	}
	for _, v := range t {
		r.Tools = append(r.Tools, v.Name)
	}
	p.mu.Lock()
	p.Requests = append(p.Requests, r)
	p.mu.Unlock()
	return p.Provider.Complete(ctx, m, t)
}

type outcome struct {
	Name, Answer, Error string
	Milliseconds        int64
	Correct             bool
	Usage               agent.Usage
	Requests            []request
	Calls               []agent.ToolCallEvent
}

func main() {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		panic("model absolute-output.json")
	}
	model, out := os.Args[1], os.Args[2]
	if model != "qwen3.8-27b-uncensored" && model != "muse-spark-1.3-contributor-free" {
		panic("unsupported model")
	}
	if !filepath.IsAbs(out) {
		panic("absolute output required")
	}
	root := filepath.Join(filepath.Dir(out), "repo-"+strings.TrimSuffix(filepath.Base(out), ".json"))
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
	outcomes := []outcome{}
	cases := []string{"lookup-auto", "lookup-orchestrator", "reuse", "current"}
	if len(os.Args) == 4 {
		cases = strings.Split(os.Args[3], ",")
	}
	for _, name := range cases {
		r := runCase(root, name, backend, caps)
		outcomes = append(outcomes, r)
		b, _ := json.MarshalIndent(map[string]any{"model": model, "cases": outcomes}, "", "  ")
		must(os.WriteFile(out, b, 0600))
		fmt.Printf("%s correct=%v requests=%d tools=%d ms=%d error=%s\n", name, r.Correct, len(r.Requests), len(r.Calls), r.Milliseconds, r.Error)
		if r.Error != "" {
			break
		}
	}
}
func runCase(base, name string, backend llm.Provider, caps *llm.CapabilityRegistry) outcome {
	root := filepath.Join(base, name)
	must(os.MkdirAll(filepath.Join(root, "src"), 0700))
	file := filepath.Join(root, "src/retry.go")
	must(os.WriteFile(file, []byte("package retry\n\nfunc RetryDelay() int {\n return 235\n}\n\nconst MaxAttempts = 7\n"), 0600))
	// Decoy files make looking up the symbol more useful than reading every file.
	for i := 0; i < 12; i++ {
		must(os.WriteFile(filepath.Join(root, "src", fmt.Sprintf("module%d.go", i)), []byte(fmt.Sprintf("package retry\nconst Module%d = %d\n", i, i)), 0600))
	}
	store, err := session.OpenStore(filepath.Join(root, "data"))
	must(err)
	defer store.Close()
	sess, err := store.Create(root, "efficiency-fixture", name)
	must(err)
	writer := session.NewWriter(store, sess.ID)
	baseReg := tools.NewRegistry()
	for _, spec := range []tools.Tool{tools.NewReadLines(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewReadContext(root).Spec(), tools.NewSearchCode(root).Spec(), tools.NewListDir(root).Spec(), tools.NewSearchHistory(store).Spec()} {
		baseReg.MustRegister(spec)
		baseReg.MarkAlwaysOn(spec.Name)
	}
	baseReg.MustRegister(tools.NewToolSearcher(baseReg, nil).Spec())
	baseReg.MarkAlwaysOn("tool_search")
	provider := &measured{Provider: backend}
	hard := name == "lookup-orchestrator"
	reg := baseReg
	if hard {
		reg = agent.OrchestratorRegistry(baseReg)
	}
	prompt := "Znajdź funkcję RetryDelay w repozytorium. Podaj zwracaną wartość i ścieżkę pliku. Niczego nie zmieniaj."
	system := "Answer using repository evidence." + agent.CoordinatorPrompt()
	if hard {
		system = "Answer using repository evidence." + agent.OrchestratorPrompt()
	}
	var initial []llm.Message
	if name == "reuse" || name == "current" {
		result, e := tools.NewReadLines(root).Spec().Fn(context.Background(), json.RawMessage(`{"file":"src/retry.go","from":1,"to":20}`))
		must(e)
		must(result.Err)
		initial = []llm.Message{
			{Role: llm.RoleUser, Content: "Inspect old unrelated notes."},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Name: "read_lines", Arguments: `{"file":"obsolete.txt"}`}}},
			{Role: llm.RoleTool, ToolCallID: "old", Name: "read_lines", Content: strings.Repeat("old irrelevant notes\n", 3200)},
			{Role: llm.RoleAssistant, Content: "Old notes reviewed."},
			{Role: llm.RoleUser, Content: "Przeczytaj src/retry.go. W odpowiedzi podaj tylko ścieżkę pliku."},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "snapshot", Name: "read_lines", Arguments: `{"file":"src/retry.go","from":1,"to":20}`}}},
			{Role: llm.RoleTool, ToolCallID: "snapshot", Name: "read_lines", Content: result.Text},
			{Role: llm.RoleAssistant, Content: "Przeczytano src/retry.go."},
		}
		for _, m := range initial {
			must(writer.AppendMessage(context.Background(), m))
		}
		must(os.WriteFile(file, []byte("package retry\nfunc RetryDelay() int { return 9001 }\nconst MaxAttempts = 99\n"), 0600))
		prompt = "Plik został zmieniony po ostatnim odczycie. Podaj dokładne wartości RetryDelay i MaxAttempts z wcześniejszego odczytu oraz ścieżkę. Chodzi o stan sprzed zmiany. To pytanie o historię, niczego nie zmieniaj."
		if name == "current" {
			prompt = "Po wcześniejszym odczycie plik został zmieniony. Sprawdź aktualne wartości RetryDelay i MaxAttempts w src/retry.go. Niczego nie zmieniaj."
		}
	}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: root, MaxSteps: 6, System: system, Writer: writer, InitialMessages: initial, Orchestrator: hard})
	must(err)
	sub := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(sub, agent.BuiltinSubAgents())
	task, err := agent.NewAgentTool(sub, loop, baseReg, provider, caps, agent.NewLoop)
	must(err)
	task.MaxSteps = 6
	task.TimeoutPerStep = 90 * time.Second
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	reg.MustRegister(agent.NewSendMessageTool(task.Workers).Spec())
	reg.MarkAlwaysOn("send_message")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "efficiency-"+filepath.Base(base)+"-"+name)
	start := time.Now()
	r := outcome{Name: name}
	stream, err := loop.Run(ctx, prompt)
	if err != nil {
		r.Error = err.Error()
	} else {
		for event := range stream {
			switch e := event.(type) {
			case agent.MessageEvent:
				r.Answer += e.Text
			case agent.ToolCallEvent:
				r.Calls = append(r.Calls, e)
			case agent.DoneEvent:
				r.Usage = e.Usage
			case agent.ErrorEvent:
				r.Error = e.Err.Error()
				r.Usage = e.Usage
			}
		}
	}
	r.Milliseconds = time.Since(start).Milliseconds()
	r.Requests = provider.Requests
	r.Correct = r.Error == "" && strings.Contains(r.Answer, "235") && strings.Contains(r.Answer, "src/retry.go")
	if name == "reuse" {
		r.Correct = r.Correct && strings.Contains(r.Answer, "7")
	}
	if name == "current" {
		r.Correct = r.Error == "" && strings.Contains(r.Answer, "9001") && strings.Contains(r.Answer, "99") && strings.Contains(r.Answer, "src/retry.go")
	}
	for _, call := range r.Calls {
		if call.Name == "ctx_execute" || call.Name == "write_file" || call.Name == "edit_line" {
			r.Correct = false
		}
	}
	return r
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
