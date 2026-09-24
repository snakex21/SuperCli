package agent

// Opt-in controlled diagnostic workload on a real local model. The legacy arm
// drops RetainedText and rebuilds the failure from the preview JSON, reproducing
// the previous tail-only evidence. Prompts, schemas and model settings match.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

type captureLiveResult struct {
	Model             string   `json:"model"`
	Arm               string   `json:"arm"`
	Thin              bool     `json:"thin"`
	Correct           bool     `json:"correct"`
	EvidenceRetrieved bool     `json:"evidence_retrieved"`
	EvidenceInline    bool     `json:"evidence_inline"`
	DurationMS        int64    `json:"duration_ms"`
	ModelCalls        int      `json:"model_calls"`
	CommandCalls      int      `json:"command_calls"`
	TokensIn          int      `json:"tokens_in"`
	TokensOut         int      `json:"tokens_out"`
	ToolSequence      []string `json:"tool_sequence"`
	ToolTrace         []string `json:"tool_trace"`
	Answer            string   `json:"answer"`
	Error             string   `json:"error,omitempty"`
}

func TestCommandCaptureAB_Live(t *testing.T) {
	baseURL, model := os.Getenv("SUPERCLI_CAPTURE_URL"), os.Getenv("SUPERCLI_CAPTURE_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("set SUPERCLI_CAPTURE_URL/MODEL for local A/B evaluation")
	}
	if !llm.IsLocalBaseURL(baseURL) {
		t.Fatal("capture evaluation requires a local endpoint")
	}
	arm := os.Getenv("SUPERCLI_CAPTURE_ARM")
	if arm != "legacy" && arm != "retained" {
		t.Fatal("SUPERCLI_CAPTURE_ARM must be legacy or retained")
	}
	result := captureLiveResult{Model: model, Arm: arm, Thin: os.Getenv("SUPERCLI_CAPTURE_ROUTE") == "thin"}
	temperature, seed := 0.0, int64(20260923)
	inner, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, MaxTokens: 2048, Timeout: 2 * time.Minute, Sampling: llm.Sampling{Temperature: &temperature, Seed: &seed}})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	provider := llm.Metered(inner, "capture-live", llm.PurposeMain, func(stat llm.CallStat) {
		mu.Lock()
		defer mu.Unlock()
		result.ModelCalls++
		result.TokensIn += stat.TokensIn
		result.TokensOut += stat.TokensOut
	})
	home := t.TempDir()
	reg := tools.NewRegistry()
	tool := tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec()
	execute := tool.Fn
	tool.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		got, err := execute(ctx, args)
		if arm == "legacy" {
			got.RetainedText = ""
			var preview ctxexec.Result
			if json.Unmarshal([]byte(got.Text), &preview) == nil && got.Err != nil && preview.ExitCode != 0 {
				got.Err = core.SelfContainedErr(errors.New(preview.FailureSummary()))
			}
		}
		return got, err
	}
	reg.MustRegister(tool)
	reg.MarkAlwaysOn("ctx_execute")
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: home, MaxSteps: 6, ThinTools: result.Thin, StableToolset: true,
		System: "Diagnose command results using the available tools. Base the answer on observed output. Keep the final answer brief. Do not modify files."})
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal([]string{os.Args[0], "-test.run=^TestCommandCaptureHelper$", "--", "--capture-fixture"})
	prompt := fmt.Sprintf("Uruchom komendę diagnostyczną (argv: %s) i ustal konkretny błąd: plik, numer linii i komunikat. Podaj przyczynę na podstawie wyniku komendy.", command)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	start := time.Now()
	events, err := loop.Run(ctx, prompt)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	var answer strings.Builder
	for event := range events {
		switch event := event.(type) {
		case ToolCallEvent:
			names[event.ID] = event.Name
			result.ToolSequence = append(result.ToolSequence, event.Name)
			result.ToolTrace = append(result.ToolTrace, event.Name+" "+event.Args)
			if event.Name == "ctx_execute" {
				result.CommandCalls++
			}
		case ToolResultEvent:
			if names[event.ID] == "ctx_execute" && event.Err != nil && strings.Contains(event.Err.Error(), "source.go:731:19: undefined: missingSymbol") {
				result.EvidenceInline = true
			}
			if names[event.ID] == "read_output" && strings.Contains(event.Output, "source.go:731:19: undefined: missingSymbol") {
				result.EvidenceRetrieved = true
			}
			if event.Err != nil {
				result.ToolTrace = append(result.ToolTrace, fmt.Sprintf("error %s: %.160s", names[event.ID], event.Err))
			}
		case MessageEvent:
			answer.WriteString(event.Text)
		case ErrorEvent:
			result.Error = event.Err.Error()
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	result.Answer = answer.String()
	result.Correct = result.Error == "" && (result.EvidenceRetrieved || result.EvidenceInline) && strings.Contains(result.Answer, "source.go") && strings.Contains(result.Answer, "731") && strings.Contains(result.Answer, "missingSymbol")
	mu.Lock()
	encoded, err := json.Marshal(result)
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CAPTURE_AB_RESULT %s", encoded)
	// A legacy failure is a measurement, not a failing test. Transport failures
	// and missed evidence are recorded for both arms, never hidden by a retry.
}
