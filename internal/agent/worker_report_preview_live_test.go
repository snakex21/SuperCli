package agent

// Opt-in A/B replay of a synthetic completed worker report through the real
// coordinator Loop. Only read_output is available; models cannot execute code,
// edit files, inspect the user's repo or access the private session snapshot.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func workerReportLiveFixture() string {
	var report strings.Builder
	report.WriteString("# Review\n\n## Verdict\nIssues need follow-up. Findings and their priorities are listed below.\n\n## Architecture\n")
	report.WriteString(strings.Repeat("The synthetic application accepts requests and passes them through its internal services.\n", 65))
	titles := []string{
		"[P1] F-101 Expired cache entries are served",
		"[P1] F-102 Canceled requests keep running",
		"[P1] F-103 Worker results lose error details",
		"[P2] F-104 Duplicate reads repeat disk work",
		"[P2] F-105 Search ignores the requested directory",
		"[P2] F-106 Output clipping hides diagnostics",
		"[P2] F-107 Restarted tasks lose progress",
		"[P3] F-108 Sidebar order uses creation time",
		"[P3] F-109 Help labels use mixed languages",
	}
	for _, title := range titles {
		fmt.Fprintf(&report, "\n### %s\n", title)
		report.WriteString(strings.Repeat("Static inspection found the behavior in the corresponding fixture module; a focused regression check is proposed.\n", 6))
	}
	report.WriteString("\n## Proposed validation\n")
	report.WriteString(strings.Repeat("Exercise the fixture boundary and assert the result before and after the proposed repair. These are proposed checks only.\n", 18))
	report.WriteString("\n## Verification status\nNOT_RUN. Static inspection only; no tests, builds or live acceptance checks were executed.\n")
	return report.String()
}

type workerReportLiveResult struct {
	ToolIDs             []string
	Wire                []workerReportWireTrace
	Model, Arm          string
	PreviewBytes        int
	Thin                bool
	ModelCalls          int
	TokensIn, TokensOut int
	Tools               []string
	ToolArgs            []string
	DurationMS          int64
	Answer              string
	FoundIDs            []string
	Correct             bool
	PrioritiesCorrect   bool
	Error               string
}

type workerReportWireTrace struct {
	Native []llm.ToolCall
	Text   string
}

type workerReportWireProvider struct {
	llm.Provider
	record func(workerReportWireTrace)
}

func (p *workerReportWireProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	source, err := p.Provider.Complete(ctx, messages, tools)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.Delta)
	go func() {
		defer close(out)
		var trace workerReportWireTrace
		var content strings.Builder
		defer func() {
			trace.Text = content.String()
			p.record(trace)
		}()
		for delta := range source {
			if delta.ToolCall != nil {
				trace.Native = append(trace.Native, *delta.ToolCall)
			}
			content.WriteString(delta.Content)
			select {
			case out <- delta:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func TestWorkerReportPreviewAB_Live(t *testing.T) {
	base, model, outDir := os.Getenv("SUPERCLI_REPORT_URL"), os.Getenv("SUPERCLI_REPORT_MODEL"), os.Getenv("SUPERCLI_REPORT_OUT")
	if base == "" || model == "" || outDir == "" {
		t.Skip("set SUPERCLI_REPORT_URL/MODEL/OUT for the controlled live replay")
	}
	local := base == "http://127.0.0.1:1234/v1"
	if !local && !(base == "https://opencode.ai/zen/v1" && strings.HasSuffix(model, "-free")) {
		t.Fatal("only the local endpoint or free Zen models are allowed")
	}
	if !filepath.IsAbs(outDir) {
		t.Fatal("absolute artifact directory required")
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		t.Fatal(err)
	}
	order := os.Getenv("SUPERCLI_REPORT_ORDER")
	if order == "" {
		order = "legacy,outline"
	}
	for _, arm := range strings.Split(order, ",") {
		if arm != "legacy" && arm != "outline" {
			t.Fatal("order must contain only legacy/outline")
		}
		t.Run(arm, func(t *testing.T) {
			result := workerReportLiveResult{Model: model, Arm: arm, Thin: os.Getenv("SUPERCLI_REPORT_ROUTE") == "thin"}
			var mu sync.Mutex
			defer func() {
				mu.Lock()
				defer mu.Unlock()
				data, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outDir, arm+".json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				t.Log(string(data))
			}()
			caps := llm.NewCapabilityRegistry()
			caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
			_ = llm.SetReasoningEffort("low")
			var provider llm.Provider
			var err error
			if local {
				temperature, seed := 0.0, int64(20260926)
				provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, Capabilities: caps, MaxTokens: 2048, Timeout: 120 * time.Second,
					Sampling: llm.Sampling{Temperature: &temperature, Seed: &seed}})
			} else {
				provider, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: base, Model: model, Capabilities: caps, Timeout: 120 * time.Second})
			}
			if err != nil {
				result.Error = err.Error()
				return
			}
			provider = llm.Metered(provider, "worker-report-replay", llm.PurposeMain, func(stat llm.CallStat) {
				mu.Lock()
				defer mu.Unlock()
				result.ModelCalls++
				result.TokensIn += stat.TokensIn
				result.TokensOut += stat.TokensOut
			})
			if os.Getenv("SUPERCLI_REPORT_TRACE") == "1" {
				provider = &workerReportWireProvider{Provider: provider, record: func(trace workerReportWireTrace) {
					mu.Lock()
					defer mu.Unlock()
					result.Wire = append(result.Wire, trace)
				}}
			}
			reg := tools.NewRegistry()
			reg.EnsureReadOutput()
			w := &Worker{ID: "worker-1", Agent: "review", Status: "done"}
			handoff := workerResult(w, workerReportLiveFixture(), nil)
			if arm == "legacy" {
				handoff.ModelPreview = ""
			}

			visible := reg.ModelResultContent("task", handoff)
			result.PreviewBytes = len(visible)
			loop, err := NewLoop(LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: t.TempDir(), MaxSteps: 6, ThinTools: result.Thin,
				System: "Summarize the supplied evidence accurately. Keep the final answer concise.", StableToolset: true})
			if err != nil {
				result.Error = err.Error()
				return
			}
			loop.Messages = append(loop.Messages,
				llm.Message{Role: llm.RoleUser, Content: "Przejrzyj syntetyczną aplikację i przygotuj raport ustaleń."},
				llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "review", Name: "task", Arguments: "{\"agent\":\"review\",\"prompt\":\"Review the synthetic application.\"}"}}},
				llm.Message{Role: llm.RoleTool, ToolCallID: "review", Name: "task", Content: visible},
				llm.Message{Role: llm.RoleAssistant, Content: "Raport workera jest gotowy."},
			)
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			ctx = llm.WithOpenCodeSession(ctx, "report-replay-"+filepath.Base(outDir)+"-"+arm)
			start := time.Now()
			events, err := loop.Run(ctx, "Wypisz wszystkie ustalenia z raportu: identyfikator, priorytet i krótki opis. Na końcu podaj zapisany w raporcie status weryfikacji.")
			if err != nil {
				result.Error = err.Error()
				return
			}
			var answer strings.Builder
			for event := range events {
				switch e := event.(type) {
				case MessageEvent:
					answer.WriteString(e.Text)
				case ToolCallEvent:
					result.ToolIDs = append(result.ToolIDs, e.ID)
					result.Tools = append(result.Tools, e.Name)
					result.ToolArgs = append(result.ToolArgs, e.Args)
					answer.Reset()
				case ErrorEvent:
					result.Error = e.Err.Error()
				}
			}
			result.DurationMS = time.Since(start).Milliseconds()
			result.Answer = strings.TrimSpace(stripThinking(answer.String()))
			for i := 101; i <= 109; i++ {
				id := fmt.Sprintf("F-%d", i)
				if strings.Contains(result.Answer, id) {
					result.FoundIDs = append(result.FoundIDs, id)
				}
			}
			matchedPriorities := 0
			for i := 101; i <= 109; i++ {
				priority := "P1"
				if i > 103 {
					priority = "P2"
				}
				if i > 107 {
					priority = "P3"
				}
				for _, line := range strings.Split(result.Answer, "\n") {
					if strings.Contains(line, fmt.Sprintf("F-%d", i)) && strings.Contains(line, priority) {
						matchedPriorities++
						break
					}
				}
			}
			result.PrioritiesCorrect = matchedPriorities == 9
			result.Correct = result.Error == "" && len(result.FoundIDs) == 9 && result.PrioritiesCorrect && strings.Contains(result.Answer, "NOT_RUN")
		})
	}
}
