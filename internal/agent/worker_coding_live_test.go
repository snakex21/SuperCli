package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

// Opt-in isolated real coding/delegation comparison. Only a synthetic workspace
// reaches the provider. Commands use the real runner, with an eval-only allowlist.
type workerCodingLiveProvider struct {
	llm.Provider
	mu            sync.Mutex
	schemaBytes   []int
	plainCommands bool
	commandViews  [][]workerCommandView
}

func (p *workerCodingLiveProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	messages, views := workerCommandRequest(messages, p.plainCommands)
	raw, _ := json.Marshal(defs)
	p.mu.Lock()
	p.commandViews = append(p.commandViews, views)
	p.schemaBytes = append(p.schemaBytes, len(raw))
	p.mu.Unlock()
	return p.Provider.Complete(ctx, messages, defs)
}

func TestWorkerCodingSchemasAB_Live(t *testing.T) {
	base, model, outDir := os.Getenv("SUPERCLI_CODING_URL"), os.Getenv("SUPERCLI_CODING_MODEL"), os.Getenv("SUPERCLI_CODING_OUT")
	if base == "" || model == "" || outDir == "" {
		t.Skip("set SUPERCLI_CODING_URL/MODEL/OUT")
	}
	local := base == "http://127.0.0.1:1234/v1"
	if !local && !(base == "https://opencode.ai/zen/v1" && strings.HasSuffix(model, "-free")) {
		t.Fatal("local or free Zen models only")
	}
	if !filepath.IsAbs(outDir) {
		t.Fatal("absolute output directory required")
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		t.Fatal(err)
	}
	order := os.Getenv("SUPERCLI_CODING_ORDER")
	if order == "" {
		order = "eager,deferred"
	}
	priorEffort := llm.ReasoningEffort()
	_ = llm.SetReasoningEffort("low")
	t.Cleanup(func() { _ = llm.SetReasoningEffort(priorEffort) })
	for _, arm := range strings.Split(order, ",") {
		if arm != "eager" && arm != "deferred" {
			t.Fatal("unknown arm")
		}
		t.Run(arm, func(t *testing.T) {
			var result struct {
				Model, Arm                  string
				Thin, PatchPreview          bool
				PlainCommands               bool
				CommandViews                [][]workerCommandView
				OmitImplementationHint      bool
				ImplementationHintPresent   bool
				Calls, TokensIn, TokensOut  int
				DurationMS                  int64
				SchemaBytes                 []int
				CallStats                   []llm.CallStat
				ToolCalls                   []llm.ToolCall
				Messages                    []llm.Message
				Correct, TestsUnchanged     bool
				UnresolvedWorkerChecks      bool
				Verification, Report, Error string
				GateRejections              int
			}
			result.Model, result.Arm, result.Thin = model, arm, os.Getenv("SUPERCLI_CODING_ROUTE") == "thin"
			result.PatchPreview = os.Getenv("SUPERCLI_CODING_PATCH_PREVIEW") != "0"
			result.PlainCommands = os.Getenv("SUPERCLI_CODING_COMMAND_TEXT") == "1"
			result.OmitImplementationHint = os.Getenv("SUPERCLI_CODING_OMIT_HINT") == "1"
			var mu sync.Mutex
			defer func() {
				raw, _ := json.MarshalIndent(result, "", "  ")
				if err := os.WriteFile(filepath.Join(outDir, arm+".json"), raw, 0600); err != nil {
					t.Error(err)
				}
				t.Logf("arm=%s correct=%t calls=%d tokens_in=%d duration_ms=%d gate_rejections=%d error=%q", arm, result.Correct, result.Calls, result.TokensIn, result.DurationMS, result.GateRejections, result.Error)
			}()
			root := filepath.Join(outDir, arm+"-workspace")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"go.mod":              "module codingfixture\n\ngo 1.22\n",
				"cache/store.go":      "package cache\n\n// Entry expires at ExpiresAt, measured in integer ticks.\ntype Entry struct { Value string; ExpiresAt int64 }\n\nfunc Lookup(entries map[string]Entry, key string, now int64) (string, bool) {\n e, ok := entries[key]\n if !ok || now > e.ExpiresAt { return \"\", false }\n return e.Value, true\n}\n",
				"cache/store_test.go": "package cache\nimport \"testing\"\nfunc TestExpiryBoundary(t *testing.T) {\n entries := map[string]Entry{\"key\": {Value:\"cached\", ExpiresAt:100}}\n for _, tc := range []struct{ now int64; want bool }{{99,true},{100,false},{101,false}} {\n value, ok := Lookup(entries,\"key\",tc.now)\n if ok != tc.want || (!ok && value != \"\") { t.Fatalf(\"now=%d value=%q hit=%t want=%t\",tc.now,value,ok,tc.want) }\n }\n if _,ok := Lookup(entries,\"missing\",50); ok { t.Fatal(\"missing key is a hit\") }\n}\n",
				"transport/retry.go":  "package transport\nfunc ShouldRetry(status int) bool { return status == 429 || status >= 500 }\n",
				"config/defaults.go":  "package config\nconst CacheLifetimeTicks = 100\n",
				"README.md":           "Small service components: cache stores values with absolute expiry ticks; transport chooses retryable statuses; config contains default values.\n",
			}
			for name, content := range files {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			check := func() ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "go", "test", "./...")
				cmd.Dir = root
				return cmd.CombinedOutput()
			}
			if output, err := check(); err == nil || !strings.Contains(string(output), "now=100") {
				t.Fatalf("fixture not red: %v %s", err, output)
			}

			caps := llm.NewCapabilityRegistry()
			caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
			var inner llm.Provider
			var err error
			if local {
				temp, seed := 0.0, int64(20260926)
				inner, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, Capabilities: caps, MaxTokens: 3072, Timeout: 120 * time.Second, Sampling: llm.Sampling{Temperature: &temp, Seed: &seed}})
			} else {
				inner, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: base, Model: model, Capabilities: caps, Timeout: 120 * time.Second})
			}
			if err != nil {
				result.Error = err.Error()
				return
			}
			provider := &workerCodingLiveProvider{plainCommands: result.PlainCommands, Provider: llm.Metered(inner, "worker-coding-eval", llm.PurposeMain, func(s llm.CallStat) {
				mu.Lock()
				defer mu.Unlock()
				result.CallStats = append(result.CallStats, s)
				result.Calls++
				result.TokensIn += s.TokensIn
				result.TokensOut += s.TokensOut
			})}
			reg := tools.NewRegistry()
			for _, spec := range []tools.Tool{
				tools.NewReadLines(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewReadContext(root).Spec(),
				tools.NewSearchCode(root).Spec(), tools.NewListDir(root).Spec(), tools.NewPatchFile(root).Spec(),
				tools.NewReadDocx(root, 0).Spec(), tools.NewEditDocx(root).Spec(), tools.NewReadXlsx(root, 0).Spec(),
				tools.NewEditXlsx(root).Spec(), tools.NewReadPdf(root, 0).Spec(), tools.NewReadZip(root, 0).Spec(),
			} {
				if !result.PatchPreview && spec.Name == "patch_file" {
					patch := spec.Fn
					spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
						out, err := patch(ctx, raw)
						// Eval-only baseline: omit the new snapshot while using
						// exactly the same patch implementation and schema.
						if i := strings.Index(out.Text, "\n[Written snapshot"); i >= 0 {
							out.Text = out.Text[:i]
						}
						return out, err
					}
				}
				reg.MustRegister(spec)
			}
			command := tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec()
			if result.PlainCommands {
				command.Description = strings.Replace(command.Description, "Output is JSON: {stdout, stderr, exit_code, truncated_stdout, truncated_stderr, duration_ms, command, workdir, error}.", "Successful complete output is text: exit code, duration, command, workdir and stdout/stderr. Large/partial output stays JSON.", 1)
			}
			runCommand := command.Fn
			command.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				var args struct {
					Command  []string
					EnvExtra []string `json:"env_extra"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return tools.Result{Err: err}, nil
				}
				joined := strings.Join(args.Command, " ")
				allowed := joined == "go test ./..." || joined == "go test ./cache" || joined == "go test -v ./..." || joined == "go test -v ./cache" || joined == "go test -count=1 ./..." || joined == "go test ./... -v" || joined == "go test ./cache -v" || joined == "go test ./... -count=1" || joined == "gofmt -w cache/store.go"
				if !allowed || len(args.EnvExtra) > 0 {
					mu.Lock()
					result.GateRejections++
					mu.Unlock()
					return tools.Result{Err: fmt.Errorf("isolated evaluation permits go test ./..., go test ./cache (optional -v), or gofmt -w cache/store.go; no command ran")}, nil
				}
				return runCommand(ctx, raw)
			}
			reg.MustRegister(command)
			reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
			parent, err := NewLoop(LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: root, ThinTools: result.Thin, StableToolset: true})
			if err != nil {
				t.Fatal(err)
			}
			specs := NewSubAgentRegistry()
			for _, spec := range BuiltinSubAgents() {
				if spec.Name == "code" {
					spec.MaxSteps = 8
					if result.OmitImplementationHint {
						spec.SkipImplementationHint = true
					}
					if arm == "eager" {
						spec.DeferredTools = nil
					}
				}
				specs.MustRegister(spec)
			}
			var child *Loop
			task, err := NewAgentTool(specs, parent, reg, provider, caps, func(cfg LoopConfig) (*Loop, error) {
				var err error
				child, err = NewLoop(cfg)
				return child, err
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
			ctx = llm.WithOpenCodeSession(ctx, "coding-eval-"+filepath.Base(outDir)+"-"+arm)
			prompt := "Pobieranie wpisu cache zwraca nieaktualną wartość dokładnie w chwili wygaśnięcia. Znajdź przyczynę, popraw kod produkcyjny i uruchom istniejące testy przez go test ./... . Nie zmieniaj testów. W krótkim raporcie podaj zmieniony plik i wynik sprawdzenia."
			args, _ := json.Marshal(map[string]any{"agent": "code", "prompt": prompt})
			// Match the production dispatcher's evidence sink without another model call.
			ctx = withWorkerInvocation(ctx, "coding-worker", nil, parent.failedChecks.observer())
			start := time.Now()
			handoff, runErr := task.execute(ctx, args)
			result.DurationMS = time.Since(start).Milliseconds()
			cancel()
			result.Report = handoff.Text
			if runErr != nil {
				result.Error = runErr.Error()
			} else if handoff.Err != nil {
				result.Error = handoff.Err.Error()
			}
			if child != nil {
				result.Messages = child.Messages
				for _, message := range child.Messages {
					if message.Role == llm.RoleUser && strings.Contains(message.Content, implementationVerificationInstruction) {
						result.ImplementationHintPresent = true
					}
					result.ToolCalls = append(result.ToolCalls, message.ToolCalls...)
				}
			}
			provider.mu.Lock()
			result.SchemaBytes = append([]int(nil), provider.schemaBytes...)
			result.CommandViews = provider.commandViews
			provider.mu.Unlock()
			output, verifyErr := check()
			result.Verification = string(output)
			originalTests, readErr := os.ReadFile(filepath.Join(root, "cache/store_test.go"))
			result.TestsUnchanged = readErr == nil && string(originalTests) == files["cache/store_test.go"]
			result.UnresolvedWorkerChecks = parent.failedChecks.unresolved()
			result.Correct = verifyErr == nil && result.TestsUnchanged && result.Error == "" && !result.UnresolvedWorkerChecks
			if !result.Correct {
				t.Errorf("worker failed independent verification: %v %s; worker=%s", verifyErr, output, result.Error)
			}
		})
	}
}
