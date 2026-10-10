package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

const codingLivePrompt = "W tym projekcie faktura błędnie nalicza koszt pozycji, która ma ilość 0. Napraw implementację. Najpierw odtwórz problem istniejącymi testami (node --test tests/invoice.test.cjs), a po poprawce uruchom te same testy ponownie. Zachowaj dotychczasowe zachowanie dla pozostałych przypadków. Nie zmieniaj testów, katalogu produktów ani innych plików niezwiązanych z błędem. Podaj przyczynę i wynik testów."

func codingLiveFixtures() map[string]string {
	return map[string]string{
		"package.json":           "{\"name\":\"invoice-fixture\",\"private\":true,\"scripts\":{\"test\":\"node --test tests/invoice.test.cjs\"}}\n",
		"README.md":              "# Invoice service\n\n`src/invoice.cjs` sums invoice lines in integer cents using the catalog and pricing modules. Omitted quantity defaults to 1; quantity 0 is valid and costs zero. Negative or noninteger quantities are rejected. Run `node --test tests/invoice.test.cjs` to check the behavior. There are no external dependencies.\n",
		"src/catalog.cjs":        "'use strict';\nconst catalog = Object.freeze({ notebook: 1250, pen: 300, folder: 450 });\nmodule.exports = { catalog };\n",
		"src/invoice.cjs":        "'use strict';\nconst { catalog } = require('./catalog.cjs');\nconst { lineTotal } = require('./pricing.cjs');\nfunction invoiceTotal(items) {\n  return items.reduce((sum, item) => sum + lineTotal(item, catalog), 0);\n}\nmodule.exports = { invoiceTotal };\n",
		"src/pricing.cjs":        "'use strict';\nfunction lineTotal(item, catalog) {\n  const unitCents = catalog[item.sku];\n  if (unitCents === undefined) throw new RangeError('Unknown SKU');\n  const quantity = item.quantity || 1;\n  if (!Number.isInteger(quantity) || quantity < 0) throw new RangeError('Invalid quantity');\n  return unitCents * quantity;\n}\nmodule.exports = { lineTotal };\n",
		"tests/invoice.test.cjs": "'use strict';\nconst test = require('node:test');\nconst assert = require('node:assert/strict');\nconst { invoiceTotal } = require('../src/invoice.cjs');\ntest('omitted quantity defaults to one', () => assert.equal(invoiceTotal([{sku:'notebook'}]), 1250));\ntest('explicit quantity multiplies unit cents', () => assert.equal(invoiceTotal([{sku:'notebook',quantity:2}]), 2500));\ntest('zero quantity costs zero', () => assert.equal(invoiceTotal([{sku:'pen',quantity:0}]), 0));\ntest('mixed invoice preserves zero and positive quantities', () => assert.equal(invoiceTotal([{sku:'notebook',quantity:2},{sku:'pen',quantity:0},{sku:'folder',quantity:3}]), 3850));\ntest('unknown SKU is rejected', () => assert.throws(() => invoiceTotal([{sku:'missing',quantity:1}]), RangeError));\ntest('negative quantity is rejected', () => assert.throws(() => invoiceTotal([{sku:'pen',quantity:-1}]), RangeError));\ntest('fractional quantity is rejected', () => assert.throws(() => invoiceTotal([{sku:'pen',quantity:1.5}]), RangeError));\n",
	}
}

type codingLiveRequestTiming struct {
	DefinitionBytes          int        `json:"definition_bytes"`
	PreflightBytes           int        `json:"preflight_bytes"`
	PreflightEstimatedTokens int        `json:"preflight_estimated_tokens"`
	DurationMS               int64      `json:"duration_ms"`
	FirstOutputMS            int64      `json:"first_output_ms"`
	Usage                    *llm.Usage `json:"usage,omitempty"`
}

// Observe the same ordinary GUI/provider path as the other opt-in evaluations.
// Every delta is passed unchanged; neither prompts nor tool results are added.
type codingLiveProvider struct {
	base                    downloadLiveProvider
	mu                      sync.Mutex
	timings                 []codingLiveRequestTiming
	cancelUnexpectedTools   context.CancelFunc
	unexpectedToolFragments int
	captureMessages         bool
	requestMessages         [][]llm.Message
	validateRequest         func(context.Context, []llm.Message, []llm.ToolDef) error
}

func (p *codingLiveProvider) Name() string         { return p.base.Name() }
func (p *codingLiveProvider) Unwrap() llm.Provider { return p.base.inner }
func (p *codingLiveProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if p.validateRequest != nil {
		if err := p.validateRequest(ctx, messages, defs); err != nil {
			return nil, err
		}
	}
	started := time.Now()
	b, _ := json.Marshal(defs)
	measurement := codingLiveRequestTiming{DefinitionBytes: len(b), FirstOutputMS: -1}
	for _, message := range messages {
		if message.Role != llm.RoleUser {
			continue
		}
		if start := strings.Index(message.Content, "Repo state (auto-collected):"); start >= 0 {
			block := strings.SplitN(message.Content[start:], "\n\n", 2)[0]
			measurement.PreflightBytes += len(block)
			measurement.PreflightEstimatedTokens += llm.EstimateTokens([]llm.Message{{Role: llm.RoleUser, Content: block}})
		}
	}
	p.mu.Lock()
	index := len(p.timings)
	p.timings = append(p.timings, measurement)
	if p.captureMessages {
		b, _ := json.Marshal(messages)
		var snapshot []llm.Message
		_ = json.Unmarshal(b, &snapshot)
		p.requestMessages = append(p.requestMessages, snapshot)
	}
	p.mu.Unlock()
	upstream, err := p.base.Complete(ctx, messages, defs)
	if err != nil {
		measurement.DurationMS = time.Since(started).Milliseconds()
		p.mu.Lock()
		p.timings[index] = measurement
		p.mu.Unlock()
		return nil, err
	}
	downstream := make(chan llm.Delta, 16)
	go func() {
		defer close(downstream)
		defer func() {
			measurement.DurationMS = time.Since(started).Milliseconds()
			p.mu.Lock()
			p.timings[index] = measurement
			p.mu.Unlock()
		}()
		for delta := range upstream {
			p.mu.Lock()
			cancelTool := p.cancelUnexpectedTools
			if delta.ToolCall != nil && cancelTool != nil {
				p.unexpectedToolFragments++
			}
			p.mu.Unlock()
			if delta.ToolCall != nil && cancelTool != nil {
				cancelTool()
				return
			}
			if measurement.FirstOutputMS < 0 && (delta.Content != "" || delta.Reasoning != "" || delta.ToolCall != nil || delta.OutputStarted || delta.ReasoningStarted) {
				measurement.FirstOutputMS = time.Since(started).Milliseconds()
			}
			if delta.Usage != nil {
				copy := *delta.Usage
				measurement.Usage = &copy
			}
			select {
			case downstream <- delta:
			case <-ctx.Done():
				return
			}
		}
	}()
	return downstream, nil
}

type codingLiveEvidence struct {
	FailedBeforeEdit        bool `json:"failed_before_edit"`
	PassedAfterEdit         bool `json:"passed_after_edit"`
	ToolsAfterCompletion    int  `json:"tools_after_completion"`
	RepeatedSuccessfulCalls int  `json:"repeated_successful_calls"`
}

type codingLiveReport struct {
	Label            string                    `json:"label"`
	Model            string                    `json:"model"`
	Run              string                    `json:"run"`
	SessionID        string                    `json:"session_id"`
	Prompt           string                    `json:"prompt"`
	Thinking         bool                      `json:"thinking"`
	ReasoningEffort  string                    `json:"reasoning_effort"`
	Fixtures         map[string]string         `json:"fixtures"`
	WallMS           int64                     `json:"wall_ms"`
	Summary          session.TurnSummary       `json:"summary"`
	Trace            chatPhaseProbeResult      `json:"trace"`
	Messages         []llm.Message             `json:"messages"`
	FinalText        string                    `json:"final_text"`
	Requests         []downloadLiveRequest     `json:"requests"`
	RequestTimings   []codingLiveRequestTiming `json:"request_timings"`
	Evidence         codingLiveEvidence        `json:"evidence"`
	FinalSources     map[string]string         `json:"final_sources"`
	InitialTest      string                    `json:"initial_test"`
	IndependentTest  string                    `json:"independent_test"`
	FixtureProtected bool                      `json:"fixture_protected"`
	Passed           bool                      `json:"passed"`
}

// A real coding task: reproduce a failing existing Node test, fix one module,
// then run the unchanged tests. The tiny project has multiple cooperating
// sources; no scripted provider or evaluation-only system guidance is used.
func TestAgentCodingLiveGUI(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SUPERCLI_CODING_LIVE_URL"))
	if baseURL == "" {
		t.Skip("opt-in local-model coding evaluation")
	}
	u, err := url.Parse(baseURL)
	model := strings.TrimSpace(os.Getenv("SUPERCLI_CODING_LIVE_MODEL"))
	if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.RawQuery != "" || u.Fragment != "" || model == "" {
		t.Fatal("requires explicit loopback model URL and model name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(cwd, "..", "..", ".tmp", "coding-efficiency-fix", "runs")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp(parent, "run-")
	if err != nil {
		t.Fatal(err)
	}
	home, data := filepath.Join(run, "workspace"), filepath.Join(run, "data")
	for _, path := range []string{home, data, filepath.Join(home, "src"), filepath.Join(home, "tests")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	report := codingLiveReport{Label: os.Getenv("SUPERCLI_CODING_LIVE_LABEL"), Model: model, Run: run, Prompt: codingLivePrompt, Thinking: true, ReasoningEffort: "max", Fixtures: codingLiveFixtures(), FinalSources: map[string]string{}}
	for name, content := range report.Fixtures {
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Own repo prevents `git -C workspace` from walking up and collecting the
	// large development checkout. Fixture identity is byte-stable across runs.
	if err := codingLiveInitRepo(home); err != nil {
		t.Fatal(err)
	}
	initial, initialErr := codingLiveRunTests(home)
	report.InitialTest = string(initial)
	if initialErr == nil || !strings.Contains(report.InitialTest, "zero quantity costs zero") || !codingLiveTestCount(report.InitialTest, "fail", 2) {
		t.Fatalf("fixture must reproduce the actual intended two failures: %v\n%s", initialErr, initial)
	}
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("thinking = true\nreasoning_effort = \"max\"\nmax_steps = 12\npreflight_repo = true\nlanguage = \"pl\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previousThinking, previousEffort := llm.ThinkingEnabled(), llm.ReasoningEffort()
	llm.SetThinkingEnabled(true)
	if err := llm.SetReasoningEffort("max"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { llm.SetThinkingEnabled(previousThinking); _ = llm.SetReasoningEffort(previousEffort) })
	cfg := echoConfig()
	cfg.Provider, cfg.BaseURL, cfg.Model = config.ProviderOpenAI, baseURL, model
	cfg.APIKey, cfg.Stream, cfg.MaxTokens, cfg.Timeout = "", true, 8192, 180*time.Second
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(cfg, home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	provider := &codingLiveProvider{base: downloadLiveProvider{inner: eng.prov}}
	eng.prov = provider
	defer func() {
		provider.base.mu.Lock()
		report.Requests = append([]downloadLiveRequest(nil), provider.base.requests...)
		provider.base.mu.Unlock()
		provider.mu.Lock()
		report.RequestTimings = append([]codingLiveRequestTiming(nil), provider.timings...)
		provider.mu.Unlock()
		report.Passed = report.Passed && !t.Failed()
		b, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(run, "receipt.json"), b, 0600)
		}
		if err != nil {
			t.Errorf("save coding receipt: %v", err)
		}
		t.Logf("coding receipt: %s", filepath.Join(run, "receipt.json"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	trace, sid := chatPhaseProbeTurn(t, NewServer(eng, false).Handler(), ctx, codingLivePrompt, "", fmt.Sprintf("coding-live-%s", filepath.Base(run)), false, true)
	report.SessionID = sid
	report.WallMS = trace.point("receipt_return") / int64(time.Millisecond)
	report.Trace = trace.result("local-qwen-gui", "coding-red-green")
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := store.ReadTurnSummaries(ctx, sid)
	if err != nil || len(summaries) == 0 {
		t.Fatalf("turn summary missing: %v", err)
	}
	report.Summary = summaries[len(summaries)-1]
	encoded, err := store.ReadMessages(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range encoded {
		msg, err := row.ToMessage()
		if err != nil {
			t.Fatal(err)
		}
		report.Messages = append(report.Messages, msg)
	}
	if len(report.Messages) > 0 {
		last := report.Messages[len(report.Messages)-1]
		if last.Role == llm.RoleAssistant {
			report.FinalText = strings.TrimSpace(regexp.MustCompile(`(?s)<(?:thinking|think)>.*?</(?:thinking|think)>`).ReplaceAllString(last.TextOnly().Content, ""))
		}
	}
	report.Evidence = codingLiveAssessEvidence(report.Messages, home)
	report.FixtureProtected = true
	for name, expected := range report.Fixtures {
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
		if err != nil {
			report.FixtureProtected = false
			continue
		}
		report.FinalSources[name] = string(b)
		if name != "src/pricing.cjs" && string(b) != expected {
			report.FixtureProtected = false
		}
	}
	finalTest, finalErr := codingLiveRunTests(home)
	report.IndependentTest = string(finalTest)
	report.Passed = report.Summary.ToolDiag.Terminal == "" && report.FinalText != "" && report.Evidence.FailedBeforeEdit && report.Evidence.PassedAfterEdit && report.FixtureProtected && finalErr == nil && codingLiveTestCount(report.IndependentTest, "tests", 7) && codingLiveTestCount(report.IndependentTest, "fail", 0) && report.FinalSources["src/pricing.cjs"] != report.Fixtures["src/pricing.cjs"]
	t.Logf("coding: %.3fs, %d model calls, %d tools, %d tool failures, red=%v green=%v, after-completion=%d", float64(report.WallMS)/1000, report.Summary.ModelCalls, report.Summary.ToolCalls, report.Summary.ToolFailures, report.Evidence.FailedBeforeEdit, report.Evidence.PassedAfterEdit, report.Evidence.ToolsAfterCompletion)
	if !report.Passed {
		t.Fatalf("coding task failed evidence or fixture preservation checks; transcript in receipt; independent test error: %v", finalErr)
	}
}

func codingLiveRunTests(home string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--test", "tests/invoice.test.cjs")
	cmd.Dir = home
	return cmd.CombinedOutput()
}

func codingLiveInitRepo(home string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, argv := range [][]string{
		{"-c", "init.templateDir=", "init", "--quiet", "-b", "main"},
		{"-c", "core.autocrlf=false", "add", "--all"},
		{"-c", "user.name=SuperCli Fixture", "-c", "user.email=fixture@supercli.invalid", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "commit", "--quiet", "-m", "Initial invoice fixture"},
	} {
		cmd := exec.CommandContext(ctx, "git", argv...)
		cmd.Dir = home
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2000-01-01T00:00:00+00:00", "GIT_COMMITTER_DATE=2000-01-01T00:00:00+00:00")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("prepare independent fixture repo: %w: %s", err, output)
		}
	}
	return nil
}

// Validation requires the installed entry script and a real explicit exit
// code. Merely reading the test source or echoing a marker never proves a run.
func codingLiveTestResult(call llm.ToolCall, message llm.Message) (int, bool) {
	if call.Name != "ctx_execute" {
		return 0, false
	}
	var args struct {
		Command []string `json:"command"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) != nil || len(args.Command) != 3 || (strings.ToLower(filepath.Base(args.Command[0])) != "node" && strings.ToLower(filepath.Base(args.Command[0])) != "node.exe") || args.Command[1] != "--test" || strings.TrimPrefix(strings.ReplaceAll(args.Command[2], `\`, "/"), "./") != "tests/invoice.test.cjs" {
		return 0, false
	}
	var result struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode *int   `json:"exit_code"`
	}
	if json.Unmarshal([]byte(message.Content), &result) == nil && result.ExitCode != nil && strings.Contains(result.Stdout+result.Stderr, "zero quantity costs zero") && codingLiveTestCount(result.Stdout+result.Stderr, "tests", 7) {
		return *result.ExitCode, true
	}
	// ctx_execute intentionally exposes failed commands as a self-contained
	// failure summary instead of duplicating the same streams as JSON.
	match := regexp.MustCompile(`^(?:error: )?command_failed exit=(-?\d+)(?:[ (:])`).FindStringSubmatch(strings.TrimSpace(message.Content))
	if len(match) != 2 || !strings.Contains(message.Content, "zero quantity costs zero") || !codingLiveTestCount(message.Content, "tests", 7) {
		return 0, false
	}
	exit, err := strconv.Atoi(match[1])
	return exit, err == nil && exit != 0
}

func codingLiveTestCount(output, key string, count int) bool {
	// Node selects TAP or its spec reporter depending on version/environment;
	// both provide an explicit terminal test-count line.
	output = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`).ReplaceAllString(output, "")
	return regexp.MustCompile(`(?m)^(?:#|ℹ)\s+` + regexp.QuoteMeta(key) + `\s+` + strconv.Itoa(count) + `\s*$`).MatchString(output)
}

func codingLiveAssessEvidence(messages []llm.Message, home string) codingLiveEvidence {
	calls := map[string]llm.ToolCall{}
	keys := map[string]string{}
	seen := map[string]bool{}
	failed, edited, completion := -1, -1, -1
	out := codingLiveEvidence{}
	for i, message := range messages {
		for _, call := range message.ToolCalls {
			calls[call.ID] = call
			var args any
			if json.Unmarshal([]byte(call.Arguments), &args) == nil {
				b, _ := json.Marshal(args)
				keys[call.ID] = call.Name + ":" + string(b)
			} else {
				keys[call.ID] = call.Name + ":" + call.Arguments
			}
		}
		if message.Role != llm.RoleTool {
			continue
		}
		call := calls[message.ToolCallID]
		code, tested := codingLiveTestResult(call, message)
		if tested && code != 0 && edited < 0 {
			failed = i
		}
		lower := strings.ToLower(strings.TrimSpace(message.Content))
		success := !strings.HasPrefix(lower, "error:")
		if call.Name == "ctx_execute" {
			var result struct {
				ExitCode *int   `json:"exit_code"`
				Error    string `json:"error"`
			}
			success = json.Unmarshal([]byte(message.Content), &result) == nil && result.ExitCode != nil && *result.ExitCode == 0 && result.Error == ""
		}
		if success && (call.Name == "patch_file" || call.Name == "write_file" || call.Name == "create_file") {
			var args struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil && codingLivePricingPath(args.Path, home) {
				edited = i
				// A repeated read or test after a real edit is verification, not
				// repeated work under an unchanged source state.
				clear(seen)
			}
		}
		if tested && code == 0 && failed >= 0 && edited > failed && i > edited {
			out.FailedBeforeEdit, out.PassedAfterEdit = true, true
			if completion < 0 {
				completion = i
			}
		}
		if success {
			key := keys[message.ToolCallID]
			if key != "" {
				if seen[key] {
					out.RepeatedSuccessfulCalls++
				}
				seen[key] = true
			}
		}
	}
	if completion >= 0 {
		after := map[string]bool{}
		for _, message := range messages[completion+1:] {
			for _, call := range message.ToolCalls {
				after[call.ID] = true
			}
			if message.Role == llm.RoleTool && message.ToolCallID != "" {
				after[message.ToolCallID] = true
			}
		}
		out.ToolsAfterCompletion = len(after)
	}
	return out
}

func codingLivePricingPath(path, home string) bool {
	path = filepath.Clean(path)
	if filepath.IsAbs(path) {
		if home == "" {
			return false
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return false
		}
		path = relative
	}
	return filepath.ToSlash(path) == "src/pricing.cjs"
}

func TestCodingLiveEvidence(t *testing.T) {
	command := func(id string, code int) (llm.ToolCall, llm.Message) {
		return llm.ToolCall{ID: id, Name: "ctx_execute", Arguments: `{"command":["node","--test","tests/invoice.test.cjs"]}`}, llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: fmt.Sprintf(`{"stdout":"zero quantity costs zero\n# tests 7","exit_code":%d}`, code)}
	}
	red, redResult := command("red", 1)
	green, greenResult := command("green", 0)
	edit := llm.ToolCall{ID: "edit", Name: "patch_file", Arguments: `{"path":"src/pricing.cjs"}`}
	good := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{red}}, redResult, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{edit}}, {Role: llm.RoleTool, ToolCallID: "edit", Content: "Patched src/pricing.cjs"}, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{green}}, greenResult}
	evidence := codingLiveAssessEvidence(good, "")
	if !evidence.FailedBeforeEdit || !evidence.PassedAfterEdit || evidence.ToolsAfterCompletion != 0 || evidence.RepeatedSuccessfulCalls != 0 {
		t.Fatalf("actual ordered red-edit-green evidence: %+v", evidence)
	}
	failedSummary := redResult
	failedSummary.Content = "error: command_failed exit=1 (0.1s)\nstdout:\nzero quantity costs zero\n# tests 7\n# fail 2"
	if exit, ok := codingLiveTestResult(red, failedSummary); !ok || exit != 1 {
		t.Fatal("real self-contained ctx_execute failure summary must prove the failed test")
	}
	green.Arguments = `{"command":["node","-e","console.log('zero quantity costs zero\\n# tests 7')"]}`
	if _, ok := codingLiveTestResult(green, greenResult); ok {
		t.Fatal("echoed output must not be accepted as running the test")
	}
	green = callsForCodingEvidence(good)["green"]
	missingExit := greenResult
	missingExit.Content = `{"stdout":"zero quantity costs zero\n# tests 7"}`
	if _, ok := codingLiveTestResult(green, missingExit); ok {
		t.Fatal("missing exit code must not prove a test run")
	}
	wrongOrder := append([]llm.Message(nil), good...)
	wrongOrder[1], wrongOrder[3] = wrongOrder[3], wrongOrder[1]
	if evidence := codingLiveAssessEvidence(wrongOrder, ""); evidence.PassedAfterEdit {
		t.Fatal("failure after editing must not count as reproducing the original bug")
	}
	noEdit := append([]llm.Message(nil), good...)
	noEdit[3].Content = "error: rejected"
	if evidence := codingLiveAssessEvidence(noEdit, ""); evidence.PassedAfterEdit {
		t.Fatal("a rejected edit must not satisfy red-edit-green")
	}
}

// Both frozen transcripts use this identical final validator. Revalidating a
// completed receipt never contacts a model and preserves the original receipt.
func TestAgentCodingLiveRevalidate(t *testing.T) {
	paths := filepath.SplitList(os.Getenv("SUPERCLI_CODING_RECEIPTS"))
	if len(paths) == 0 {
		t.Skip("opt-in validation of completed coding receipts")
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report codingLiveReport
		if err := json.Unmarshal(b, &report); err != nil {
			t.Fatal(err)
		}
		home := filepath.Join(report.Run, "workspace")
		report.Evidence = codingLiveAssessEvidence(report.Messages, home)
		report.FixtureProtected = true
		for name, original := range report.Fixtures {
			b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(name)))
			if err != nil || (name != "src/pricing.cjs" && string(b) != original) {
				report.FixtureProtected = false
			}
			report.FinalSources[name] = string(b)
		}
		output, runErr := codingLiveRunTests(home)
		report.IndependentTest = string(output)
		report.Passed = report.Evidence.FailedBeforeEdit && report.Evidence.PassedAfterEdit && report.FixtureProtected && runErr == nil && codingLiveTestCount(report.IndependentTest, "tests", 7) && codingLiveTestCount(report.IndependentTest, "fail", 0) && report.Summary.ToolDiag.Terminal == "" && report.FinalText != "" && report.FinalSources["src/pricing.cjs"] != report.Fixtures["src/pricing.cjs"]
		if !report.Passed {
			t.Fatalf("revalidated receipt failed: %s, evidence=%+v, preserved=%v, independent=%v", path, report.Evidence, report.FixtureProtected, runErr)
		}
		b, err = json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(filepath.Dir(path), "receipt-revalidated.json"), b, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("revalidated real coding receipt: %s", path)
	}
}

func callsForCodingEvidence(messages []llm.Message) map[string]llm.ToolCall {
	calls := map[string]llm.ToolCall{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			calls[call.ID] = call
		}
	}
	return calls
}
