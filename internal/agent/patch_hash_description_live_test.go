package agent

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
	"supercli/internal/tools/fileops"
)

// This opt-in experiment exposes real read/patch tools only in a temporary
// one-file workspace. ctx_execute is a checksum fixture: it never starts a
// process. No user files or private session messages reach a provider.
func TestPatchHashDescriptionAB_Live(t *testing.T) {
	base, model, outDir := os.Getenv("SUPERCLI_PATCH_URL"), os.Getenv("SUPERCLI_PATCH_MODEL"), os.Getenv("SUPERCLI_PATCH_OUT")
	if base == "" || model == "" || outDir == "" {
		t.Skip("set SUPERCLI_PATCH_URL/MODEL/OUT for the controlled live edit")
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
	order := os.Getenv("SUPERCLI_PATCH_ORDER")
	if order == "" {
		order = "legacy,optional"
	}
	priorEffort := llm.ReasoningEffort()
	_ = llm.SetReasoningEffort("low")
	t.Cleanup(func() { _ = llm.SetReasoningEffort(priorEffort) })
	for _, arm := range strings.Split(order, ",") {
		if arm != "legacy" && arm != "optional" {
			t.Fatal("unknown arm")
		}
		t.Run(arm, func(t *testing.T) {
			var result struct {
				Model, Arm                               string
				Thin                                     bool
				Calls, TokensIn, TokensOut, HashRequests int
				Tools                                    []llm.ToolCall
				DurationMS                               int64
				Answers                                  []string
				TurnCorrect                              []bool
				Error                                    string
			}
			result.Model, result.Arm, result.Thin = model, arm, os.Getenv("SUPERCLI_PATCH_ROUTE") == "thin"
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
			var provider llm.Provider
			var err error
			if local {
				temp, seed := 0.0, int64(20260926)
				provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, Capabilities: caps, MaxTokens: 2048, Timeout: 120 * time.Second, Sampling: llm.Sampling{Temperature: &temp, Seed: &seed}})
			} else {
				provider, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: base, Model: model, Capabilities: caps, Timeout: 120 * time.Second})
			}
			if err != nil {
				result.Error = err.Error()
				return
			}
			provider = llm.Metered(provider, "patch-hash-replay", llm.PurposeMain, func(stat llm.CallStat) {
				mu.Lock()
				defer mu.Unlock()
				result.Calls++
				result.TokensIn += stat.TokensIn
				result.TokensOut += stat.TokensOut
			})
			root := t.TempDir()
			body := "package retry\n\n// MaxAttempts includes the first attempt.\nconst MaxAttempts = 2\n"
			if err := os.WriteFile(filepath.Join(root, "retry.go"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewReadLines(root).Spec())
			patch := tools.NewPatchFile(root).Spec()
			legacy := "SHA-256 of current contents; rejects stale edits"
			candidate := "Optional known SHA-256; rejects stale edits"
			if arm == "legacy" {
				patch.Schema = strings.ReplaceAll(patch.Schema, candidate, legacy)
			} else {
				patch.Schema = strings.ReplaceAll(patch.Schema, legacy, candidate)
			}
			reg.MustRegister(patch)
			cmd := tools.NewCtxExecuteTool(nil, root).Spec()
			cmd.Fn = func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				var args struct{ Command []string }
				if err := json.Unmarshal(raw, &args); err != nil {
					return tools.Result{Err: err}, nil
				}
				joined := strings.ToLower(strings.Join(args.Command, " "))
				if !strings.Contains(joined, "retry.go") || (!strings.Contains(joined, "sha256") && !strings.Contains(joined, "get-filehash")) {
					return tools.Result{Err: fmt.Errorf("this isolated fixture only provides the retry.go SHA-256; no command was executed")}, nil
				}
				hash, err := fileops.FileSHA256(filepath.Join(root, "retry.go"))
				if err != nil {
					return tools.Result{Err: err}, nil
				}
				result.HashRequests++
				data, _ := json.Marshal(map[string]any{"stdout": hash + "\n", "stderr": "", "exit_code": 0, "duration_ms": 0})
				return tools.Result{Text: string(data)}, nil
			}
			reg.MustRegister(cmd)
			for _, name := range []string{"read_lines", "patch_file", "ctx_execute"} {
				reg.MarkAlwaysOn(name)
			}
			loop, err := NewLoop(LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: root, MaxSteps: 6, ThinTools: result.Thin, StableToolset: true, System: "Complete the requested code edit accurately. Keep the final answer concise."})
			if err != nil {
				result.Error = err.Error()
				return
			}
			start := time.Now()
			for turn, prompt := range []string{"W pliku retry.go zwiększ MaxAttempts z 2 do 3. Zachowaj pozostałą treść.", "Zmień teraz MaxAttempts w retry.go z 3 do 4. Zachowaj pozostałą treść."} {
				ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
				ctx = llm.WithOpenCodeSession(ctx, "patch-replay-"+filepath.Base(outDir)+"-"+arm)
				events, err := loop.Run(ctx, prompt)
				if err != nil {
					cancel()
					result.Error = err.Error()
					return
				}
				var answer strings.Builder
				for event := range events {
					switch e := event.(type) {
					case ToolCallEvent:
						result.Tools = append(result.Tools, llm.ToolCall{ID: e.ID, Name: e.Name, Arguments: e.Args})
						answer.Reset()
					case MessageEvent:
						answer.WriteString(e.Text)
					case ErrorEvent:
						result.Error = e.Err.Error()
					}
				}
				cancel()
				result.Answers = append(result.Answers, strings.TrimSpace(stripThinking(answer.String())))
				actual, err := os.ReadFile(filepath.Join(root, "retry.go"))
				want := strings.Replace(body, "MaxAttempts = 2", fmt.Sprintf("MaxAttempts = %d", turn+3), 1)
				result.TurnCorrect = append(result.TurnCorrect, err == nil && string(actual) == want)
				if result.Error != "" {
					break
				}
			}
			result.DurationMS = time.Since(start).Milliseconds()
		})
	}
}
