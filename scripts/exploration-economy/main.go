// Controlled, read-only experiment for repo discovery and worker continuation.
// All artifacts are written under the explicitly supplied output directory.
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
	"supercli/internal/tools"
)

type call struct {
	Phase       string
	Name        string
	Args        json.RawMessage
	OutputBytes int
	Error       string
}

func main() {
	if len(os.Args) != 3 {
		panic("model absolute-result.json")
	}
	model, outfile := os.Args[1], os.Args[2]
	if model != "qwen3.8-27b-uncensored" && model != "muse-spark-1.3-contributor-free" {
		panic("unsupported experiment model")
	}
	if !filepath.IsAbs(outfile) {
		panic("absolute output path required")
	}
	root := filepath.Join(filepath.Dir(outfile), "repo")
	sources := []string{
		"cmd/server/main.go", "src/auth/login.go", "src/auth/token.go",
		"src/billing/invoice.go", "src/billing/receipt.go", "src/storage/cache.go",
		"src/storage/sqlite.go", "src/sync/pull.go", "src/sync/push.go",
	}
	fixture := append(append([]string{}, sources...), ".zig-cache/hash/generated.go", "node_modules/example/generated.go")
	for _, name := range fixture {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(p, []byte("package example\n"), 0600); err != nil {
			panic(err)
		}
	}
	result := map[string]any{"model": model, "fixture": root}
	defer func() {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(outfile, b, 0600); err != nil {
			panic(err)
		}
		fmt.Println(string(b))
	}()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	_ = llm.SetReasoningEffort("low")
	var provider llm.Provider
	var err error
	if strings.HasSuffix(model, "-free") {
		provider, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, Timeout: 120 * time.Second})
	} else {
		provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: model, HTTPClient: &http.Client{}, Capabilities: caps, MaxTokens: 2048, Timeout: 120 * time.Second})
	}
	if err != nil {
		result["error"] = err.Error()
		return
	}
	reg := tools.NewRegistry()
	calls := []call{}
	phase := "explore"
	var mu sync.Mutex
	for _, spec := range []tools.Tool{tools.NewListDir(root).Spec(), tools.NewSearchCode(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewReadLines(root).Spec()} {
		fn, name := spec.Fn, spec.Name
		spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			res, err := fn(ctx, args)
			c := call{Phase: phase, Name: name, Args: append(json.RawMessage(nil), args...), OutputBytes: len(res.Text)}
			if err != nil {
				c.Error = err.Error()
			} else if res.Err != nil {
				c.Error = res.Err.Error()
			}
			mu.Lock()
			calls = append(calls, c)
			mu.Unlock()
			return res, err
		}
		reg.MustRegister(spec)
		reg.MarkAlwaysOn(spec.Name)
	}
	sub := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(sub, agent.BuiltinSubAgents())
	task, err := agent.NewAgentTool(sub, nil, reg, provider, caps, func(cfg agent.LoopConfig) (*agent.Loop, error) {
		cfg.BaseDir = root
		return agent.NewLoop(cfg)
	})
	if err != nil {
		result["error"] = err.Error()
		return
	}
	task.MaxSteps = 10
	task.TimeoutPerStep = 120 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "exploration-economy-"+filepath.Base(outfile))
	prompt := "Podaj wszystkie ścieżki plików Go w src/ oraz cmd/, pogrupowane katalogami. Wystarczą nazwy i ścieżki, bez treści plików."
	raw, _ := json.Marshal(map[string]any{"agent": "explore", "prompt": prompt})
	start := time.Now()
	res, err := task.Spec().Fn(ctx, raw)
	result["explore_ms"] = time.Since(start).Milliseconds()
	result["explore_report"] = res.Text
	if err != nil {
		result["error"] = err.Error()
	} else if res.Err != nil {
		result["error"] = res.Err.Error()
	}
	worker, ok := task.Workers.Get("worker-1")
	if !ok {
		result["calls"] = calls
		return
	}
	before := worker.Snapshot()
	result["explore_state"] = before
	result["history_messages_before"] = len(worker.Loop.Messages)
	missing := []string{}
	for _, p := range sources {
		if !strings.Contains(res.Text, p) {
			missing = append(missing, p)
		}
	}
	result["missing_paths"] = missing
	result["explore_correct"] = len(missing) == 0 && res.Err == nil && err == nil
	if before.Status == "done" {
		phase = "followup"
		raw, _ = json.Marshal(map[string]string{"to": "worker-1", "message": "Które dwie ścieżki znalezionych plików należą do modułu auth? Podaj tylko ścieżki."})
		start = time.Now()
		follow, err := agent.NewSendMessageTool(task.Workers).Spec().Fn(ctx, raw)
		result["followup_ms"] = time.Since(start).Milliseconds()
		result["followup_report"] = follow.Text
		result["followup_correct"] = strings.Contains(follow.Text, "src/auth/login.go") && strings.Contains(follow.Text, "src/auth/token.go") && err == nil && follow.Err == nil
		if err != nil {
			result["followup_error"] = err.Error()
		} else if follow.Err != nil {
			result["followup_error"] = follow.Err.Error()
		}
		after := worker.Snapshot()
		result["followup_steps"] = after.Steps - before.Steps
		result["followup_input"] = after.TokensIn - before.TokensIn
		result["followup_output"] = after.TokensOut - before.TokensOut
		result["history_messages_after"] = len(worker.Loop.Messages)
	}
	result["calls"] = calls
	result["history"] = worker.Loop.Messages
	spec := tools.NewListDir(root).Spec()
	b, _ := json.Marshal(llm.ToolDef{Name: spec.Name, Description: spec.Description, Schema: spec.Schema})
	result["schema_bytes"] = len(b)
}
