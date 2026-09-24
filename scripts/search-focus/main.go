// Read-only synthetic search experiment. It never registers shell or edit tools.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		panic("model output-directory")
	}
	model, dir := os.Args[1], os.Args[2]
	if model != "qwen3.8-27b-uncensored" && model != "muse-spark-1.3-contributor-free" {
		panic("unsupported experiment model")
	}
	if !filepath.IsAbs(dir) {
		panic("absolute output directory required")
	}
	if err := os.MkdirAll(filepath.Join(dir, "repo", "src"), 0700); err != nil {
		panic(err)
	}
	root := filepath.Join(dir, "repo")
	fixture := map[string]string{
		"README.md":       "Go project. Source lives under src. Markdown includes generated documentation and historical examples.\n",
		"a-generated.md":  strings.Repeat("ResolveWidget(key) returns an old example value, not executable Go.\n", 180),
		"src/a-legacy.md": strings.Repeat("ResolveWidget is discussed here without current implementation.\n", 180),
		"src/widget.go":   "package widget\n\n// ResolveWidget returns a backend identifier.\nfunc ResolveWidget(key string) string {\n if key == \"alpha\" { return \"atlas-731\" }\n return \"fallback-29\"\n}\n",
	}
	for name, body := range fixture {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			panic(err)
		}
	}
	result := map[string]any{"model": model}
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(result) }()
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	_ = llm.SetReasoningEffort("low")
	client := &http.Client{}
	var p llm.Provider
	var err error
	if strings.HasSuffix(model, "-free") {
		p, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: model, HTTPClient: client, Capabilities: caps, Timeout: 120 * time.Second})
	} else {
		p, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: model, HTTPClient: client, Capabilities: caps, MaxTokens: 3072, Timeout: 120 * time.Second})
	}
	if err != nil {
		result["error"] = err.Error()
		return
	}
	reg := tools.NewRegistry()
	for _, tool := range []tools.Tool{tools.NewSearchCode(root).Spec(), tools.NewReadLines(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewListDir(root).Spec()} {
		reg.MustRegister(tool)
		reg.MarkAlwaysOn(tool.Name)
	}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: p, Registry: reg, MaxSteps: 8, BaseDir: root, System: "Inspect this synthetic repository using the available read-only tools. Answer from executable code."})
	if err != nil {
		result["error"] = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	ctx = llm.WithOpenCodeSession(ctx, "search-focus-"+filepath.Base(dir))
	start := time.Now()
	events, err := loop.Run(ctx, "Znajdź implementację ResolveWidget w kodzie Go i podaj dokładnie: ścieżkę pliku, wartość zwracaną dla alpha i wartość dla nieznanego klucza. Odpowiedz krótko.")
	if err != nil {
		result["error"] = err.Error()
		return
	}
	var answer strings.Builder
	calls := []agent.ToolCallEvent{}
	outputs := []map[string]any{}
	for event := range events {
		switch e := event.(type) {
		case agent.MessageEvent:
			answer.WriteString(e.Text)
		case agent.ToolCallEvent:
			calls = append(calls, e)
		case agent.ToolResultEvent:
			outputs = append(outputs, map[string]any{"id": e.ID, "output": e.Output, "error": fmt.Sprint(e.Err)})
		case agent.DoneEvent:
			result["usage"] = e.Usage
			result["steps"] = e.Steps
		case agent.ErrorEvent:
			result["error"] = e.Err.Error()
			result["usage"] = e.Usage
			result["steps"] = e.Steps
		}
	}
	text := answer.String()
	result["ms"] = time.Since(start).Milliseconds()
	result["answer"] = text
	result["calls"] = calls
	result["outputs"] = outputs
	result["correct"] = strings.Contains(text, "src/widget.go") && strings.Contains(text, "atlas-731") && strings.Contains(text, "fallback-29")
	spec := tools.NewSearchCode(root).Spec()
	schema, _ := json.Marshal(llm.ToolDef{Name: spec.Name, Description: spec.Description, Schema: spec.Schema})
	result["schema_bytes"] = len(schema)
}
