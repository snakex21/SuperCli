package agent

// Opt-in end-to-end workload for a real OpenAI-compatible backend. Each trial
// gets a fresh Go project with one deterministic bug. The model must inspect,
// edit and test it through the production Loop/tool protocol; the harness then
// verifies the workspace independently. No prompt or model output is persisted.
//
// Required:
//
//   SUPERCLI_EVAL_TOOL_URL / SUPERCLI_EVAL_TOOL_MODEL
//
// Optional:
//
//   SUPERCLI_EVAL_TOOL_TRIALS (default 10, maximum 50)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

type liveToolTrial struct {
	Trial        int              `json:"trial"`
	Passed       bool             `json:"passed"`
	DurationMS   int64            `json:"duration_ms"`
	Turns        stats.Total      `json:"turns"`
	PhasesUS     map[string]int64 `json:"phases_us"`
	ModelCalls   int              `json:"model_calls"`
	ToolCalls    int              `json:"tool_calls"`
	ToolFailures int              `json:"tool_failures"`
	ToolSequence []string         `json:"tool_sequence,omitempty"`
	FailedTools  []string         `json:"failed_tools,omitempty"`
	ToolTrace    []string         `json:"tool_trace,omitempty"`
	Error        string           `json:"error,omitempty"`
}

func TestToolWorkload_Live(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_EVAL_TOOL_URL"))
	model := strings.TrimSpace(os.Getenv("SUPERCLI_EVAL_TOOL_MODEL"))
	if baseURL == "" || model == "" {
		t.Skip("live tool workload: set SUPERCLI_EVAL_TOOL_URL and SUPERCLI_EVAL_TOOL_MODEL")
	}
	trials := 10
	if raw := strings.TrimSpace(os.Getenv("SUPERCLI_EVAL_TOOL_TRIALS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 50 {
			trials = n
		}
	}

	results := make([]liveToolTrial, 0, trials)
	for i := 1; i <= trials; i++ {
		result := runLiveToolTrial(t, i, baseURL, model)
		results = append(results, result)
		b, _ := json.Marshal(result)
		t.Log(string(b))
	}

	var passed, modelCalls, toolCalls, toolFailures int
	var durationMS, backendUS, streamUS, cliUS, toolsUS int64
	for _, result := range results {
		if result.Passed {
			passed++
		}
		modelCalls += result.ModelCalls
		toolCalls += result.ToolCalls
		toolFailures += result.ToolFailures
		durationMS += result.DurationMS
		backendUS += result.PhasesUS[stats.PhaseBackendWait]
		streamUS += result.PhasesUS[stats.PhaseStreamTotal]
		cliUS += result.PhasesUS[stats.PhaseContextPrepare] + result.PhasesUS[stats.PhaseNextTurnPrepare]
		toolsUS += result.PhasesUS[stats.PhaseToolExecution]
	}
	summary := map[string]any{
		"trials": trials, "passed": passed, "success_percent": passed * 100 / trials,
		"average_duration_ms": durationMS / int64(trials),
		"average_model_calls": float64(modelCalls) / float64(trials),
		"average_tool_calls":  float64(toolCalls) / float64(trials),
		"tool_failures":       toolFailures,
		"backend_wait_ms":     backendUS / 1000, "stream_ms": streamUS / 1000,
		"cli_ms": cliUS / 1000, "tools_ms": toolsUS / 1000,
	}
	b, _ := json.Marshal(summary)
	t.Logf("WORKLOAD_SUMMARY %s", b)
	if passed != trials {
		t.Fatalf("live tool workload passed %d/%d trials", passed, trials)
	}
}

func runLiveToolTrial(t *testing.T, trial int, baseURL, model string) liveToolTrial {
	t.Helper()
	result := liveToolTrial{Trial: trial, PhasesUS: map[string]int64{}}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "go.mod"), []byte("module workload\n\ngo 1.22\n"), 0o644); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := os.WriteFile(filepath.Join(home, "mathutil.go"), []byte("package workload\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"), 0o644); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := os.WriteFile(filepath.Join(home, "mathutil_test.go"), []byte("package workload\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 { t.Fatalf(\"Add(2, 3) = %d, want 5\", got) }\n}\n"), 0o644); err != nil {
		result.Error = err.Error()
		return result
	}

	recorder := stats.NewMemory()
	inner, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, Timeout: 5 * time.Minute})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	provider := llm.Metered(inner, "local-tool-workload", llm.PurposeMain, func(s llm.CallStat) {
		recorder.RecordCall(stats.Call{Purpose: s.Purpose, Model: s.Model, Provider: s.Provider,
			TTFTUs: s.TTFT.Microseconds(), DurationUs: s.Duration.Microseconds(), TokensIn: s.TokensIn,
			TokensOut: s.TokensOut, Failed: s.Failed, Canceled: s.Canceled})
	})
	registry := tools.NewRegistry()
	for _, spec := range []tools.Tool{
		tools.NewReadLines(home).Spec(), tools.NewReadContext(home).Spec(), tools.NewReadMany(home).Spec(),
		tools.NewListDir(home).Spec(), tools.NewSearchCode(home).Spec(), tools.NewEditLine(home).Spec(),
		tools.NewEditLines(home).Spec(), tools.NewWriteFile(home).Spec(),
		tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec(),
	} {
		registry.MustRegister(spec)
		registry.MarkAlwaysOn(spec.Name)
	}
	loop, err := NewLoop(LoopConfig{
		Provider: provider, Registry: registry, BaseDir: home, Stats: recorder, MaxSteps: 8,
		System:    "You are a coding agent. Use tools to inspect and modify the workspace. Keep the final answer brief and factual.",
		ThinTools: true, StableToolset: true,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	ch, err := loop.Run(ctx, "Test w mathutil_test.go nie przechodzi z powodu błędu w mathutil.go. Dla oszczędności tur odczytaj oba pliki jednym read_many, napraw wyłącznie kod produkcyjny i uruchom go test ./... przez ctx_execute. Nie zmieniaj testu.")
	toolNames := map[string]string{}
	verifiedByTool := false
	if err == nil {
		for event := range ch {
			switch e := event.(type) {
			case ToolCallEvent:
				result.ToolCalls++
				result.ToolSequence = append(result.ToolSequence, e.Name)
				result.ToolTrace = append(result.ToolTrace, fmt.Sprintf("call %s %s", e.Name, e.Args))
				toolNames[e.ID] = e.Name
			case ToolResultEvent:
				if e.Err != nil {
					result.ToolFailures++
					failure := fmt.Sprintf("%s: %v", toolNames[e.ID], e.Err)
					result.FailedTools = append(result.FailedTools, failure)
					result.ToolTrace = append(result.ToolTrace, "error "+failure)
				} else if toolNames[e.ID] == "ctx_execute" {
					// This workload measures time-to-verified-change, not the optional
					// prose summary after a green test. Cancel that final model call so a
					// looping local model cannot dominate ten otherwise complete trials.
					verifiedByTool = true
					cancel()
				}
			case ErrorEvent:
				if e.Err != nil && !verifiedByTool {
					err = e.Err
				}
			}
		}
	}
	cancel()
	result.DurationMS = time.Since(started).Milliseconds()
	result.Turns = stats.Sum(recorder.Snapshot())
	result.PhasesUS = stats.SumPhases(recorder.Snapshot())
	for _, call := range stats.SumCalls(recorder.Calls()) {
		result.ModelCalls += call.Count
	}
	if err != nil && !verifiedByTool {
		result.Error = err.Error()
		return result
	}

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer verifyCancel()
	cmd := exec.CommandContext(verifyCtx, "go", "test", "./...")
	cmd.Dir = home
	if output, verifyErr := cmd.CombinedOutput(); verifyErr != nil {
		result.Error = fmt.Sprintf("independent go test: %v: %s", verifyErr, strings.TrimSpace(string(output)))
		return result
	}
	production, readErr := os.ReadFile(filepath.Join(home, "mathutil.go"))
	if readErr != nil || !strings.Contains(string(production), "return a + b") {
		result.Error = fmt.Sprintf("production fix missing: read=%v content=%q", readErr, string(production))
		return result
	}
	result.Passed = true
	return result
}
