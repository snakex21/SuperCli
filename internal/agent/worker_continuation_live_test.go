package agent

import (
	"context"
	"encoding/json"
	"fmt"
	goformat "go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/llm/prompt"
	"supercli/internal/system/config"
	"supercli/internal/system/execution"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

// Opt-in end-to-end fixture: initial multi-file repair, then a new requirement
// through send_message on the same worker. Only synthetic project data is sent.
type continuationReplayProvider struct {
	llm.Provider
	mu             sync.Mutex
	requests       []continuationRequest
	seed           []llm.Message
	toolHint       string
	dispatcherHint string
}

func (p *continuationReplayProvider) Unwrap() llm.Provider { return p.Provider }

type continuationRequest struct {
	Scripted              bool
	View                  json.RawMessage
	ToolNames             []string
	Tools                 []llm.ToolDef
	Estimate              int
	PrunedResults         int
	Messages, ToolResults int
	HasInitialTask        bool
	HasFollowup           bool
}

func (p *continuationReplayProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if p.toolHint != "" {
		hint := continuationNativeFirstHint
		if p.toolHint == "legacy" {
			hint = continuationLegacyToolHint
		}
		messages = append([]llm.Message(nil), messages...)
		for i := range messages {
			if messages[i].Role == llm.RoleSystem {
				messages[i].Content = strings.Replace(messages[i].Content, prompt.ThinToolProtocol, hint, 1)
			}
		}
	}
	if p.dispatcherHint == "legacy" {
		defs = append([]llm.ToolDef(nil), defs...)
		for i := range defs {
			if defs[i].Name == invokeToolName {
				_, eligible, hasCatalog := strings.Cut(defs[i].Description, " Eligible: ")
				defs[i].Description = continuationLegacyDispatcherHint
				if hasCatalog {
					defs[i].Description += " Eligible: " + eligible
				}
			}
		}
	}
	r := continuationRequest{Messages: len(messages), Estimate: estimateRequestTokens(messages, defs), Tools: append([]llm.ToolDef(nil), defs...)}
	r.View, _ = json.Marshal(messages)
	for _, def := range defs {
		r.ToolNames = append(r.ToolNames, def.Name)
	}
	for _, m := range messages {
		if m.Role == llm.RoleTool {
			r.ToolResults++
			if strings.HasPrefix(m.Content, pruneMarkerPrefix) {
				r.PrunedResults++
			}
		}
		text := m.Content
		for _, part := range m.Parts {
			text += part.Text
		}
		r.HasInitialTask = r.HasInitialTask || strings.Contains(text, "kolejce odświeżeń")
		r.HasFollowup = r.HasFollowup || strings.Contains(text, "pomijaj puste klucze")
	}
	p.mu.Lock()
	if len(p.seed) > 0 {
		reply := p.seed[0]
		p.seed = p.seed[1:]
		r.Scripted = true
		p.requests = append(p.requests, r)
		p.mu.Unlock()
		return replayContinuationReply(ctx, reply)
	}
	p.requests = append(p.requests, r)
	p.mu.Unlock()
	return p.Provider.Complete(ctx, messages, defs)
}

func TestWorkerContinuationWorkflow_Live(t *testing.T) {
	base, model, outDir := os.Getenv("SUPERCLI_CONTINUATION_URL"), os.Getenv("SUPERCLI_CONTINUATION_MODEL"), os.Getenv("SUPERCLI_CONTINUATION_OUT")
	if base == "" || model == "" || outDir == "" {
		t.Skip("set SUPERCLI_CONTINUATION_URL/MODEL/OUT")
	}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: model, Reasoning: true, ReasoningKnown: true, ToolUse: true, Source: llm.SourceProvider})
	profile, err := continuationExecutionProfile(base, model, caps, os.Getenv("SUPERCLI_CONTINUATION_PROTOCOL"), os.Getenv("SUPERCLI_CONTINUATION_CATALOG"))
	if err != nil {
		t.Fatal(err)
	}
	thin := profile.ThinTools
	hint := os.Getenv("SUPERCLI_CONTINUATION_TOOL_HINT")
	if hint != "" && hint != "native-first" && hint != "legacy" {
		t.Fatal("evaluation tool hint must be empty, native-first or legacy")
	}
	dispatcherHint := os.Getenv("SUPERCLI_CONTINUATION_DISPATCHER_HINT")
	if dispatcherHint != "" && dispatcherHint != "legacy" {
		t.Fatal("evaluation dispatcher hint must be empty or legacy")
	}
	seed, err := loadContinuationSeed(os.Getenv("SUPERCLI_CONTINUATION_SEED"), model)
	if err != nil {
		t.Fatal(err)
	}
	window := 0
	if raw := os.Getenv("SUPERCLI_CONTINUATION_WINDOW"); raw != "" {
		window, err = strconv.Atoi(raw)
		if err != nil || window < 4096 || window > 262144 {
			t.Fatal("evaluation window must be between 4096 and 262144")
		}
	}
	local := base == "http://127.0.0.1:1234/v1"
	if !local && !(base == "https://opencode.ai/zen/v1" && strings.HasSuffix(model, "-free")) {
		t.Fatal("local or free Zen models only")
	}
	if !filepath.IsAbs(outDir) {
		t.Fatal("absolute artifact directory required")
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(outDir, "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                  "module continuationfixture\n\ngo 1.22\n",
		"README.md":               "# Refresh service\n\nqueue selects refresh jobs in their validity window. service publishes selected keys. transport chooses retryable HTTP responses. metrics counts completed operations.\n",
		"queue/queue.go":          "package queue\n\ntype Entry struct {\n Key string\n ReadyAt, ExpiresAt int64\n}\n\n// Ready returns entries ready for dispatch at now, in input order.\nfunc Ready(entries []Entry, now int64) []Entry {\n var ready []Entry\n for _, entry := range entries {\n  if now >= entry.ReadyAt && now <= entry.ExpiresAt {\n   ready = append(ready, entry)\n  }\n }\n return ready\n}\n",
		"queue/queue_test.go":     "package queue\n\nimport \"testing\"\n\nfunc TestReadyWindow(t *testing.T) {\n entries:=[]Entry{{Key:\"ready\",ReadyAt:10,ExpiresAt:20},{Key:\"future\",ReadyAt:21,ExpiresAt:30}}\n for _,tc:=range []struct{ now int64; count int }{{9,0},{10,1},{19,1},{20,0},{21,1},{30,0}} {\n  if got:=Ready(entries,tc.now);len(got)!=tc.count {t.Fatalf(\"now=%d: got %v, want %d ready jobs\",tc.now,got,tc.count)}\n }\n}\n",
		"service/publish.go":      "package service\n\nimport (\n \"strings\"\n \"continuationfixture/queue\"\n)\n\nfunc Publish(entries []queue.Entry, now int64, send func(string)) {\n for _,entry:=range queue.Ready(entries,now) {\n  send(strings.ToLower(entry.Key))\n }\n}\n",
		"service/publish_test.go": "package service\n\nimport (\n \"reflect\"\n \"testing\"\n \"continuationfixture/queue\"\n)\n\nfunc TestPublishCanonicalKeys(t *testing.T) {\n entries:=[]queue.Entry{{Key:\"  ALPHA \\t\",ReadyAt:1,ExpiresAt:10},{Key:\"BeTa\",ReadyAt:1,ExpiresAt:10}}\n var got []string\n Publish(entries,2,func(key string){got=append(got,key)})\n if want:=[]string{\"alpha\",\"beta\"};!reflect.DeepEqual(got,want){t.Fatalf(\"keys=%q want=%q\",got,want)}\n}\n",
		"transport/retry.go":      "package transport\n\nfunc Retryable(status int) bool { return status==429 || status>=500 }\n",
		"metrics/counter.go":      "package metrics\n\ntype Counter struct{ Completed int }\nfunc (c *Counter) RecordCompletion() { c.Completed++ }\n",
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
	format := exec.Command("gofmt", "-w", "queue/queue.go", "queue/queue_test.go", "service/publish.go", "service/publish_test.go", "transport/retry.go", "metrics/counter.go")
	format.Dir = root
	if out, err := format.CombinedOutput(); err != nil {
		t.Fatalf("format fixture: %v %s", err, out)
	}
	fixedTests := map[string][]byte{}
	for _, name := range []string{"queue/queue_test.go", "service/publish_test.go"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		fixedTests[name] = body
	}
	check := func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "test", "./...")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if output, err := check(); err == nil || !strings.Contains(output, "TestReadyWindow") || !strings.Contains(output, "TestPublishCanonicalKeys") {
		t.Fatalf("initial fixture not red for both defects: %v %s", err, output)
	}
	type stageResult struct {
		ContextWindow                                                   int
		LastResult                                                      string
		Runs                                                            int
		Name, Report, Error, Verification                               string
		DurationMS                                                      int64
		Calls, TokensIn, TokensOut, ToolCalls, Failures, GateRejections int
		Correct, TestsUnchanged                                         bool
		Snapshot                                                        Snapshot
		Messages                                                        []llm.Message
		Stats                                                           []llm.CallStat
	}
	result := struct {
		ToolHint          string
		DispatcherHint    string
		SeedHistorySHA256 string
		Model             string
		Thin              bool
		StableToolset     bool
		CatalogHoist      bool
		NewLoops          int
		Stages            []stageResult
		Requests          []continuationRequest
	}{Model: model, Thin: thin, StableToolset: profile.StableToolset, CatalogHoist: profile.CatalogHoist, ToolHint: hint, DispatcherHint: dispatcherHint}
	if seed != nil {
		result.SeedHistorySHA256 = seed.SHA256
	}
	defer func() {
		raw, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(filepath.Join(outDir, "result.json"), raw, 0600); err != nil {
			t.Error(err)
		}
	}()
	client, err := newContinuationTraceClient(outDir)
	if err != nil {
		t.Fatal(err)
	}
	var inner llm.Provider
	if local {
		temp, seed := 0.0, int64(20260927)
		inner, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: model, Capabilities: caps, MaxTokens: 4096, Timeout: 120 * time.Second, HTTPClient: client, Sampling: llm.Sampling{Temperature: &temp, Seed: &seed}})
	} else {
		inner, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: base, Model: model, Capabilities: caps, Timeout: 120 * time.Second, HTTPClient: client})
	}
	if err != nil {
		t.Fatal(err)
	}
	priorEffort := llm.ReasoningEffort()
	_ = llm.SetReasoningEffort("low")
	t.Cleanup(func() { _ = llm.SetReasoningEffort(priorEffort) })
	var mu sync.Mutex
	var callStats []llm.CallStat
	gateRejections := 0
	provider := &continuationReplayProvider{Provider: llm.Metered(inner, "continuation-eval", llm.PurposeMain, func(s llm.CallStat) { mu.Lock(); callStats = append(callStats, s); mu.Unlock() })}
	provider.toolHint = hint
	provider.dispatcherHint = dispatcherHint
	if seed != nil {
		provider.seed = append([]llm.Message(nil), seed.Replies...)
	}
	reg := tools.NewRegistry()
	for _, spec := range []tools.Tool{
		tools.NewReadLines(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewReadContext(root).Spec(),
		tools.NewSearchCode(root).Spec(), tools.NewListDir(root).Spec(), tools.NewPatchFile(root).Spec(),
		tools.NewReadDocx(root, 0).Spec(), tools.NewEditDocx(root).Spec(), tools.NewReadXlsx(root, 0).Spec(),
		tools.NewEditXlsx(root).Spec(), tools.NewReadPdf(root, 0).Spec(), tools.NewReadZip(root, 0).Spec(),
	} {
		reg.MustRegister(spec)
	}
	command := tools.NewCtxExecuteTool(ctxexec.New(root), root).Spec()
	runCommand := command.Fn
	command.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
		var args struct {
			Command  []string
			EnvExtra []string `json:"env_extra"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return tools.Result{Err: err}, nil
		}
		allowed := continuationEvalCommandAllowed(root, args.Command) && len(args.EnvExtra) == 0
		if !allowed {
			mu.Lock()
			gateRejections++
			mu.Unlock()
			return tools.Result{Err: fmt.Errorf("isolated evaluation permits go test ./... (or ./queue, ./service; optional -v, -count=1, -run), go fmt ./..., and gofmt -w on production Go files; no command ran")}, nil
		}
		return runCommand(ctx, raw)
	}
	reg.MustRegister(command)
	reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
	parent, err := NewLoop(LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: root, ThinTools: thin, StableToolset: profile.StableToolset, CatalogHoist: profile.CatalogHoist, WindowFor: func(string) int { return window }})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	for _, spec := range BuiltinSubAgents() {
		if spec.Name == "code" {
			spec.MaxSteps = 16
		}
		specs.MustRegister(spec)
	}
	var child *Loop
	task, err := NewAgentTool(specs, parent, reg, provider, caps, func(cfg LoopConfig) (*Loop, error) {
		result.NewLoops++
		if cfg.ThinTools != profile.ThinTools || cfg.StableToolset != profile.StableToolset || cfg.CatalogHoist != profile.CatalogHoist {
			return nil, fmt.Errorf("worker tool/cache profile differs from requested profile")
		}
		var err error
		child, err = NewLoop(cfg)
		if err == nil && window > 0 && child.ContextWindow() != window {
			return nil, fmt.Errorf("requested worker window %d, got %d", window, child.ContextWindow())
		}
		return child, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for stage := 0; stage < 2; stage++ {
		name, prompt := "initial", "W kolejce odświeżeń są dwa błędy: zadanie jest wysyłane także dokładnie w chwili wygaśnięcia, a opublikowane klucze zawierają otaczające spacje. Znajdź przyczyny, popraw kod produkcyjny i uruchom istniejące testy przez go test ./... . Nie zmieniaj testów. Podaj krótko zmienione pliki i wynik sprawdzenia."
		if stage == 1 {
			if seed != nil {
				// Replay verified the fixture. Freeze its exact original history for
				// both arms; command timings/paths must not change the comparison.
				history := seed.History
				for len(history) > 0 && history[0].Role == llm.RoleSystem {
					history = history[1:]
				}
				child.LoadConversation(history)
				child.rememberContextModel(context.Background())
			}
			name, prompt = "followup", "Kontynuuj tę samą pracę: pomijaj puste klucze po usunięciu otaczających białych znaków. Zachowaj kolejność pozostałych kluczy i wcześniejsze poprawki. Dodałem service/empty_key_test.go z nowym przypadkiem. Popraw kod produkcyjny, uruchom go test ./... i podaj wynik. Nie zmieniaj testów."
			extra := []byte("package service\nimport(\"reflect\";\"testing\";\"continuationfixture/queue\")\nfunc TestEmptyKeysSkipped(t *testing.T){entries:=[]queue.Entry{{Key:\" \\t\",ReadyAt:1,ExpiresAt:10},{Key:\" FIRST \",ReadyAt:1,ExpiresAt:10},{Key:\"\",ReadyAt:1,ExpiresAt:10},{Key:\"Second\",ReadyAt:1,ExpiresAt:10}};var got []string;Publish(entries,2,func(key string){got=append(got,key)});if want:=[]string{\"first\",\"second\"};!reflect.DeepEqual(got,want){t.Fatalf(\"keys=%q want=%q\",got,want)}}\n")
			extra, err = goformat.Source(extra)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "service/empty_key_test.go"), extra, 0600); err != nil {
				t.Fatal(err)
			}
			fixedTests["service/empty_key_test.go"] = extra
			output, checkErr := check()
			if checkErr == nil || !strings.Contains(output, "TestEmptyKeysSkipped") {
				t.Fatalf("followup fixture not red: %v %s", checkErr, output)
			}
		}
		mu.Lock()
		statsStart, gatesStart := len(callStats), gateRejections
		mu.Unlock()
		provider.mu.Lock()
		requestsStart := len(provider.requests)
		provider.mu.Unlock()
		messageStart := 0
		if child != nil {
			messageStart = len(child.Messages)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
		ctx = llm.WithOpenCodeSession(ctx, "continuation-eval-"+filepath.Base(outDir))
		start := time.Now()
		var reply tools.Result
		var runErr error
		if stage == 0 {
			args, _ := json.Marshal(map[string]any{"agent": "code", "prompt": prompt})
			reply, runErr = task.execute(ctx, args)
		} else {
			workers := task.Workers.List()
			if len(workers) != 1 {
				cancel()
				t.Fatalf("workers=%d", len(workers))
			}
			args, _ := json.Marshal(map[string]any{"to": workers[0].ID, "message": prompt})
			reply, runErr = NewSendMessageTool(task.Workers).execute(ctx, args)
		}
		elapsed := time.Since(start).Milliseconds()
		cancel()
		saved := stageResult{Name: name, Report: reply.Text, DurationMS: elapsed, TestsUnchanged: true}
		if runErr != nil {
			saved.Error = runErr.Error()
		} else if reply.Err != nil {
			saved.Error = reply.Err.Error()
		}
		workers := task.Workers.List()
		if len(workers) == 1 {
			saved.Snapshot = workers[0].Snapshot()
			workers[0].stateMu.RLock()
			saved.LastResult, saved.Runs = workers[0].LastResult, workers[0].Runs
			workers[0].stateMu.RUnlock()
		}
		if child != nil {
			saved.ContextWindow = child.ContextWindow()
			saved.Messages = append([]llm.Message(nil), child.Messages...)
			for _, message := range child.Messages[messageStart:] {
				saved.ToolCalls += len(message.ToolCalls)
				if message.Role == llm.RoleTool && strings.HasPrefix(message.Content, "error:") {
					saved.Failures++
				}
			}
		}
		mu.Lock()
		saved.Stats = append([]llm.CallStat(nil), callStats[statsStart:]...)
		saved.GateRejections = gateRejections - gatesStart
		mu.Unlock()
		for _, stat := range saved.Stats {
			saved.TokensIn += stat.TokensIn
			saved.TokensOut += stat.TokensOut
		}
		provider.mu.Lock()
		saved.Calls = len(provider.requests) - requestsStart
		result.Requests = append([]continuationRequest(nil), provider.requests...)
		provider.mu.Unlock()
		output, verifyErr := check()
		saved.Verification = output
		for name, want := range fixedTests {
			got, err := os.ReadFile(filepath.Join(root, name))
			saved.TestsUnchanged = saved.TestsUnchanged && err == nil && string(got) == string(want)
		}
		saved.Correct = verifyErr == nil && saved.TestsUnchanged && saved.Error == "" && strings.TrimSpace(saved.LastResult) != "" && saved.Snapshot.Status == "done"
		result.Stages = append(result.Stages, saved)
		t.Logf("stage=%s calls=%d tools=%d failures=%d gates=%d input=%d duration_ms=%d correct=%v error=%q", name, saved.Calls, saved.ToolCalls, saved.Failures, saved.GateRejections, saved.TokensIn, saved.DurationMS, saved.Correct, saved.Error)
		if !saved.Correct && stage == 0 {
			t.Fatalf("initial stage failed verification: %v %s; %s", verifyErr, output, saved.Error)
		}
		if !saved.Correct {
			t.Errorf("stage %s failed verification: %v %s; %s", name, verifyErr, output, saved.Error)
		}
	}
	if result.NewLoops != 1 {
		t.Errorf("continuation rebuilt worker: %d loops", result.NewLoops)
	}
	if len(result.Stages) != 2 || result.Stages[1].Runs != 2 {
		t.Error("worker did not resume twice")
	}
}

func continuationEvalCommandAllowed(root string, argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	if argv[0] == "go" && argv[1] == "test" {
		for i := 2; i < len(argv); i++ {
			switch argv[i] {
			case "./...", "./queue", "./queue/", "./service", "./service/", "-v", "-count=1":
			case "-run":
				i++
				if i >= len(argv) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if len(argv) == 3 && argv[0] == "go" && argv[1] == "fmt" && argv[2] == "./..." {
		return true
	}
	if argv[0] == "gofmt" && argv[1] == "-w" && len(argv) > 2 {
		for _, arg := range argv[2:] {
			full := arg
			if !filepath.IsAbs(full) {
				full = filepath.Join(root, arg)
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return false
			}
			rel = filepath.ToSlash(rel)
			if rel != "queue/queue.go" && rel != "service/publish.go" && rel != "transport/retry.go" && rel != "metrics/counter.go" {
				return false
			}
		}
		return true
	}
	return false
}

// Frozen evaluation variants permit reproducing the comparison after rollout.
const continuationNativeFirstHint = `Use native tool calls when available. Text fallback:
«tool_name
key: value
»
One block per call; separate blocks for independent calls. For no arguments: «tool_name».
Text arguments have no JSON braces. Arrays/objects require native JSON tool calling; patch_file's old/new shorthand accepts text.
Simple read-only catalog tools can skip tool_search: call invoke_tool with "tool: name" and one "arg.field: value" line per target argument.`

const continuationLegacyToolHint = `Calling tools — use this exact format, not JSON:
« then the tool name on its own line, then one "key: value" per line, then ». Example:
«list_dir
path: .
»
For a tool with no arguments write «tool_name» on one line. Do not wrap arguments in JSON or braces. Use separate blocks for independent calls in one response.
Tools that need arrays/objects use native JSON tool calling, not this sentinel form; patch_file's old/new shorthand does not.
Simple read-only catalog tools can skip tool_search: call invoke_tool with "tool: name" and one "arg.field: value" line per target argument.`

const continuationLegacyDispatcherHint = "Schema-stable tool dispatcher. Simple read-only tools work immediately. For a complex or mutating tool, call tool_search once to activate it, then use invoke_tool with target arguments in args (or arg.<name> fields); target execution still uses its normal validation and safety controls."

// continuationExecutionProfile uses the same defaults as GUI/TUI. Explicit
// protocol/catalog overrides retain the earlier experiments without silently
// making the slower tail placement the default for future measurements.
func continuationExecutionProfile(base, model string, caps *llm.CapabilityRegistry, protocol, catalog string) (execution.Profile, error) {
	profile := execution.Resolve(config.Config{BaseURL: base, Model: model}, config.TomlConfig{}, caps, false)
	switch protocol {
	case "", "profile":
	case "thin", "native":
		profile.ThinTools = protocol == "thin"
		profile.CatalogHoist = profile.ThinTools && profile.StableToolset
	default:
		return profile, fmt.Errorf("evaluation protocol must be profile, thin or native")
	}
	switch catalog {
	case "", "profile":
	case "tail":
		profile.CatalogHoist = false
	case "hoist":
		if !profile.ThinTools || !profile.StableToolset {
			return profile, fmt.Errorf("hoisted catalog requires thin tools and a stable toolset")
		}
		profile.CatalogHoist = true
	default:
		return profile, fmt.Errorf("evaluation catalog must be profile, tail or hoist")
	}
	return profile, nil
}
