// Command run compares retained and dropped historical reasoning using only
// synthetic fixtures. The existing Go probe owns the unchanged HTTP protocol.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"supercli/internal/testbrowser"
)

const systemPrompt = "You are repairing a small JavaScript module. Preserve the requested API. Work only with the supplied fixture."

type model struct {
	Model     string
	Base      string
	Transport string
}

var models = []model{
	{"qwen3.8-27b-uncensored", "http://127.0.0.1:1234/v1", "chat"},
	{"mimo-v2.6-flash-free", "https://opencode.ai/zen/v1", "chat"},
	{"muse-spark-1.3-contributor-free", "https://opencode.ai/zen/v1", "responses"},
	{"deepseek-v4-flash-free", "https://opencode.ai/zen/v1", "chat"},
}

type fixtureTest struct {
	Args []any `json:"args"`
	Want any   `json:"want"`
}

type fixture struct {
	ID     string
	Fn     string
	File   string
	Source string
	Spec   string
	Tests  []fixtureTest
}

func fixtures() []fixture {
	// JSON keeps the JavaScript inputs, nulls, booleans and empty arrays exact.
	const escapedTests = `[
		{"args":[""],"want":[""]}, {"args":[","],"want":["",""]},
		{"args":["a,,b,"],"want":["a","","b",""]},
		{"args":["a\\,b,c"],"want":["a,b","c"]},
		{"args":["a\\\\,b"],"want":["a\\","b"]},
		{"args":["x\\"],"want":["x\\"]},
		{"args":["\\q, z "],"want":["q"," z "]},
		{"args":["\\,"],"want":[","]},
		{"args":["a\\\\\\,b,c"],"want":["a\\,b","c"]}
	]`
	const cacheTests = `[
		{"args":[[{"op":"get","key":"x","t":0}],10],"want":[null]},
		{"args":[[{"op":"put","key":"x","t":0,"value":4},{"op":"get","key":"x","t":9},{"op":"get","key":"x","t":10}],10],"want":[4,null]},
		{"args":[[{"op":"put","key":"x","t":0,"value":4},{"op":"get","key":"x","t":9},{"op":"get","key":"x","t":15}],10],"want":[4,null]},
		{"args":[[{"op":"put","key":"x","t":0,"value":4},{"op":"put","key":"x","t":8,"value":7},{"op":"get","key":"x","t":10},{"op":"get","key":"x","t":18}],10],"want":[7,null]},
		{"args":[[{"op":"put","key":"x","t":1,"value":0},{"op":"get","key":"x","t":1}],0],"want":[null]},
		{"args":[[{"op":"put","key":"x","t":0,"value":false},{"op":"put","key":"y","t":1,"value":""},{"op":"delete","key":"x","t":2},{"op":"get","key":"x","t":3},{"op":"get","key":"y","t":3}],5],"want":[null,""]},
		{"args":[[],10],"want":[]}
	]`
	decode := func(data string) []fixtureTest {
		var tests []fixtureTest
		if err := json.Unmarshal([]byte(data), &tests); err != nil {
			panic(err) // A malformed built-in fixture is a programming error.
		}
		return tests
	}
	return []fixture{
		{
			ID: "escaped-fields", Fn: "splitEscaped", File: "parser.js",
			Source: "function splitEscaped(s) { return s.split(\",\"); }",
			Spec:   "splitEscaped(s) returns comma-delimited fields. A backslash escapes the next character, including commas and backslashes. Preserve empty fields, including a trailing empty field. A dangling final backslash is a literal backslash. Empty input returns one empty field. Do not trim whitespace.",
			Tests:  decode(escapedTests),
		},
		{
			ID: "cache-expiry", Fn: "runCache", File: "cache.js",
			Source: "function runCache(events, ttl) { const m = new Map(), out = []; for (const e of events) { if (e.op === \"put\") m.set(e.key, {value:e.value, expires:e.t+ttl}); else if (e.op === \"delete\") m.delete(e.key); else if (e.op === \"get\") out.push(m.has(e.key) ? m.get(e.key).value : null); } return out; }",
			Spec:   "runCache(events, ttl) simulates a TTL cache and returns one value for every get, in input order. Events are {op:\"put\"|\"get\"|\"delete\", key, t, value?}; times are nondecreasing. put replaces value and sets expiry to t+ttl. get is a miss (null) when absent or t >= expiry; get never refreshes expiry. ttl <= 0 means immediately expired. delete removes a key. Each call is independent. No wall clock or asynchronous work.",
			Tests:  decode(cacheTests),
		},
	}
}

type config struct {
	root, outdir string
	models       []model
	fixtures     []fixture
}

func parseArgs(root string, args []string) (config, error) {
	outputRoot := filepath.Join(root, ".tmp", "history-eval")
	name := "experiment"
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	outdir := name
	if !filepath.IsAbs(outdir) {
		outdir = filepath.Join(outputRoot, outdir)
	}
	outdir, err := filepath.Abs(outdir)
	if err != nil {
		return config{}, err
	}
	rel, err := filepath.Rel(outputRoot, outdir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return config{}, errors.New("Output must remain under .tmp/history-eval")
	}
	cfg := config{root: root, outdir: outdir, models: []model{}, fixtures: []fixture{}}
	for _, m := range models {
		if len(args) < 2 || args[1] == "" || contains(strings.Split(args[1], ","), m.Model) {
			cfg.models = append(cfg.models, m)
		}
	}
	for _, f := range fixtures() {
		if len(args) < 3 || args[2] == "" || args[2] == f.ID {
			cfg.fixtures = append(cfg.fixtures, f)
		}
	}
	// Like the original CLI, additional positional arguments are ignored.
	return cfg, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func repositoryRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	executable, _ := os.Executable()
	for _, start := range []string{cwd, filepath.Dir(executable)} {
		for dir := start; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				if _, err := os.Stat(filepath.Join(dir, "scripts", "reasoning-history", "probe", "main.go")); err == nil {
					return dir, nil
				}
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return "", errors.New("run from the application/repository root (go.mod and scripts/reasoning-history/probe required)")
}

type message = map[string]any

type probeInput struct {
	model
	History []message
	Session string
	Effort  string
}

type toolCall struct{ ID, Name, Arguments string }
type usage struct{ Input, Output, Reasoning, CachedInput *int64 }
type probeResult struct {
	Text, Reasoning string
	Calls           []toolCall
	Usage           *usage
	Error           string
	TTFTMS          int64
	ElapsedMS       int64
	RequestBytes    int
	SSE             string
}

type probeFunc func(context.Context, probeInput) (json.RawMessage, error)

func commandProbe(root string) probeFunc {
	return processProbe(func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, filepath.Join(root, ".tmp", "history-eval", "probe.exe"))
		cmd.Dir = root
		return cmd
	})
}

func processProbe(command func(context.Context) *exec.Cmd) probeFunc {
	return func(ctx context.Context, in probeInput) (json.RawMessage, error) {
		data, err := jsonBytes(in, false)
		if err != nil {
			return nil, err
		}
		cmd := command(ctx)
		cmd.Stdin = bytes.NewReader(data)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return nil, fmt.Errorf("probe exit %d: %s", exit.ExitCode(), stderr.String())
			}
			return nil, err
		}
		if !json.Valid(stdout.Bytes()) {
			return nil, errors.New(stdout.String() + stderr.String())
		}
		return bytes.Clone(bytes.TrimSpace(stdout.Bytes())), nil
	}
}

var (
	eventSeparator = regexp.MustCompile(`\r?\n\r?\n`)
	eventLine      = regexp.MustCompile(`\r?\n`)
	thinkingTags   = regexp.MustCompile(`(?i)<think>[\s\S]*?</think>|<thinking>[\s\S]*?</thinking>|<reasoning>[\s\S]*?</reasoning>|<reflection>[\s\S]*?</reflection>`)
)

func jsSpace(r rune) bool {
	return r >= '\t' && r <= '\r' || r == ' ' || r == 0xa0 || r == 0x1680 ||
		r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f ||
		r == 0x205f || r == 0x3000 || r == 0xfeff
}

func events(sse string) []message {
	var events []message
	for _, block := range eventSeparator.Split(sse, -1) {
		var data []string
		for _, line := range eventLine.Split(block, -1) {
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimLeftFunc(value, jsSpace))
			}
		}
		var event message
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &event) == nil && event != nil {
			events = append(events, event)
		}
	}
	return events
}

func reply(m model, result probeResult) ([]message, error) {
	if m.Transport == "responses" {
		var done []message
		var completed []message
		foundCompleted := false
		for _, event := range events(result.SSE) {
			if event["type"] == "response.output_item.done" {
				item, _ := event["item"].(map[string]any)
				done = append(done, item)
			}
			if event["type"] == "response.completed" && !foundCompleted {
				foundCompleted = true
				response, _ := event["response"].(map[string]any)
				output, _ := response["output"].([]any)
				for _, value := range output {
					item, _ := value.(map[string]any)
					completed = append(completed, item)
				}
			}
		}
		if len(done) > 0 {
			return done, nil
		}
		if len(completed) == 0 {
			return nil, errors.New("Missing native Responses output")
		}
		return completed, nil
	}
	assistant := message{"role": "assistant", "content": result.Text}
	if result.Reasoning != "" {
		assistant["reasoning_content"] = result.Reasoning
	}
	if len(result.Calls) > 0 {
		var calls []any
		for _, call := range result.Calls {
			calls = append(calls, message{"id": call.ID, "type": "function", "function": message{"name": call.Name, "arguments": call.Arguments}})
		}
		assistant["tool_calls"] = calls
	}
	return []message{assistant}, nil
}

func cloneHistory(history []message) []message {
	data, _ := json.Marshal(history)
	var result []message
	_ = json.Unmarshal(data, &result)
	return result
}

func stripCompleted(history []message) []message {
	var result []message
	strip := func(text string) string { return strings.TrimFunc(thinkingTags.ReplaceAllString(text, ""), jsSpace) }
	for _, item := range cloneHistory(history) {
		if item["type"] == "reasoning" {
			continue
		}
		if item["role"] == "assistant" {
			delete(item, "reasoning_content")
			delete(item, "reasoning_details")
			switch content := item["content"].(type) {
			case string:
				item["content"] = strip(content)
			case []any:
				parts := []any{}
				for _, part := range content {
					p, _ := part.(map[string]any)
					if p["type"] == "thinking" || p["type"] == "redacted_thinking" {
						continue
					}
					if text, ok := p["text"].(string); ok {
						p["text"] = strip(text)
					}
					parts = append(parts, part)
				}
				item["content"] = parts
			}
		}
		result = append(result, item)
	}
	return result
}

type stats struct {
	Calls          int      `json:"calls"`
	ToolCalls      int      `json:"toolCalls"`
	Input          int64    `json:"input"`
	Output         int64    `json:"output"`
	Reasoning      int64    `json:"reasoning"`
	Cached         int64    `json:"cached"`
	MS             int64    `json:"ms"`
	TTFT           int64    `json:"ttft"`
	ReasoningChars int      `json:"reasoningChars"`
	Errors         []string `json:"errors"`
}

type turnResult struct {
	Stats stats  `json:"stats"`
	Text  string `json:"text"`
}

type verification struct {
	Passed  int             `json:"passed"`
	Total   int             `json:"total"`
	Success bool            `json:"success"`
	Got     json.RawMessage `json:"got,omitempty"`
	Error   *string         `json:"error,omitempty"`
}

type verifyFunc func(context.Context, fixture, string) (verification, error)

type runner struct {
	cfg    config
	probe  probeFunc
	verify verifyFunc
	stdout io.Writer
	seq    int
}

func jsonBytes(value any, pretty bool) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func (r *runner) write(file string, value any) error {
	data, err := jsonBytes(value, true)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.cfg.outdir, file), data, 0o644)
}

func (r *runner) log(value any) error {
	data, err := jsonBytes(value, false)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(r.stdout, string(data))
	return err
}

func (r *runner) call(ctx context.Context, m model, history []message, session, label string) (probeResult, error) {
	in := probeInput{model: m, History: history, Session: session, Effort: "low"}
	raw, err := r.probe(ctx, in)
	if err != nil {
		return probeResult{}, err
	}
	var result probeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	r.seq++
	if err := r.write(fmt.Sprintf("%03d-%s.json", r.seq, label), struct {
		Input  probeInput      `json:"input"`
		Result json.RawMessage `json:"result"`
	}{in, raw}); err != nil {
		return result, err
	}
	event := message{"event": "request-complete", "model": m.Model, "label": label, "ms": result.ElapsedMS, "ttft": result.TTFTMS}
	if result.Usage != nil {
		if result.Usage.Input != nil {
			event["input"] = *result.Usage.Input
		}
		if result.Usage.Output != nil {
			event["output"] = *result.Usage.Output
		}
	}
	if result.Error != "" {
		event["error"] = result.Error
	}
	return result, r.log(event)
}

func number(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func (r *runner) turn(ctx context.Context, m model, history *[]message, session string, f fixture, label string) (turnResult, error) {
	result := turnResult{Stats: stats{Errors: []string{}}}
	for step := 0; step < 4; step++ {
		response, err := r.call(ctx, m, *history, session, fmt.Sprintf("%s-%d", label, step))
		if err != nil {
			return result, err
		}
		result.Text = response.Text
		s := &result.Stats
		s.Calls++
		if u := response.Usage; u != nil {
			s.Input += number(u.Input)
			s.Output += number(u.Output)
			s.Reasoning += number(u.Reasoning)
			s.Cached += number(u.CachedInput)
		}
		s.MS += response.ElapsedMS
		if step == 0 {
			s.TTFT = response.TTFTMS
		}
		s.ReasoningChars += len(utf16.Encode([]rune(response.Reasoning)))
		if response.Error != "" {
			s.Errors = append(s.Errors, response.Error)
			return result, nil
		}
		messages, err := reply(m, response)
		if err != nil {
			return result, err
		}
		*history = append(*history, messages...)
		if len(response.Calls) == 0 {
			return result, nil
		}
		for _, call := range response.Calls {
			s.ToolCalls++
			var args any = message{}
			var decoded any
			if json.Unmarshal([]byte(call.Arguments), &decoded) == nil {
				args = decoded
			}
			output := "No shell is available. Return the requested source as your final answer; verification is performed by the evaluator."
			if call.Name == "read" {
				if args == nil {
					return result, errors.New("Cannot read properties of null (reading 'filePath')")
				}
				object, _ := args.(map[string]any)
				output = "Unknown fixture path; available: " + f.File
				if object["filePath"] == f.File {
					output = f.Source
				}
			}
			if m.Transport == "responses" {
				*history = append(*history, message{"type": "function_call_output", "call_id": call.ID, "output": output})
			} else {
				*history = append(*history, message{"role": "tool", "tool_call_id": call.ID, "content": output})
			}
		}
	}
	result.Stats.Errors = append(result.Stats.Errors, "tool step limit")
	return result, nil
}

func (r *runner) run(ctx context.Context) error {
	if err := os.MkdirAll(r.cfg.outdir, 0o755); err != nil {
		return err
	}
	results := []any{}
	for _, m := range r.cfg.models {
		for i, f := range r.cfg.fixtures {
			session := "history-eval-" + filepath.Base(r.cfg.outdir) + "-" + m.Model + "-" + f.ID
			role := "system"
			if m.Transport == "responses" {
				role = "developer"
			}
			history := []message{
				{"role": role, "content": systemPrompt},
				{"role": "user", "content": "Read " + f.File + " and explain the smallest correction needed for this specification. Do not implement yet. Keep your visible explanation under 100 words.\n" + f.Spec},
			}
			plan, err := r.turn(ctx, m, &history, session, f, "plan-"+f.ID)
			if err != nil {
				return err
			}
			if len(plan.Stats.Errors) > 0 {
				results = append(results, message{"model": m.Model, "fixture": f.ID, "arm": "plan", "stats": plan.Stats, "text": plan.Text})
				break
			}
			arms := []string{"keep", "drop"}
			if i%2 != 0 {
				arms = []string{"drop", "keep"}
			}
			for _, arm := range arms {
				branch := cloneHistory(history)
				if arm == "drop" {
					branch = stripCompleted(history)
				}
				branch = append(branch, message{"role": "user", "content": "Implement that correction now. Return only the complete " + f.Fn + " function as JavaScript source, with no explanation and no markdown. Keep all requirements from my previous message."})
				completed, err := r.turn(ctx, m, &branch, session, f, arm+"-"+f.ID)
				if err != nil {
					return err
				}
				verified, err := r.verify(ctx, f, completed.Text)
				if err != nil {
					return err
				}
				results = append(results, message{"model": m.Model, "fixture": f.ID, "arm": arm, "plan": plan.Stats, "stats": completed.Stats, "verification": verified, "text": completed.Text})
				if err := r.log(message{"event": "arm-complete", "model": m.Model, "fixture": f.ID, "arm": arm, "stats": completed.Stats, "verification": verified}); err != nil {
					return err
				}
				if err := r.write("results.json", results); err != nil {
					return err
				}
			}
		}
	}
	return r.write("results.json", results)
}

// The model's code runs only in a fresh Worker, not the main page's event loop.
// This is fixture isolation and a time limit, not a security sandbox.
const verifyExpression = `(payload => new Promise(resolve => {
  let code = payload.text.trim();
  const fence = code.match(/^\x60{3}(?:javascript|js)?\s*\n([\s\S]*?)\n\x60{3}\s*$/i);
  if (fence) code = fence[1];
  const total = payload.tests.length;
  const failure = error => ({passed:0,total,success:false,error});
  const args = JSON.stringify(payload.tests.map(t => t.args));
  const wants = JSON.stringify(payload.tests.map(t => t.want));
  const source = "try {\n" + code +
    "\n;(got => {\nconst wants = " + wants + ";\n" +
    "const passed = wants.filter((want,i) => JSON.stringify(want) === JSON.stringify(got[i])).length;\n" +
    "self.postMessage({passed,total:wants.length,success:passed===wants.length,got});\n" +
    "})(JSON.parse(JSON.stringify(" + args + ".map(a => " + payload.fn + "(...a)))));\n" +
    "} catch(e) { self.postMessage({passed:0,total:" + total + ",success:false,error:e.message}); }";
  let worker, blobURL, timer, finished = false;
  const finish = value => {
    if (finished) return;
    finished = true;
    clearTimeout(timer);
    if (worker) worker.terminate();
    if (blobURL) URL.revokeObjectURL(blobURL);
    resolve(value);
  };
  try {
    blobURL = URL.createObjectURL(new Blob([source], {type:'text/javascript'}));
    worker = new Worker(blobURL);
    timer = setTimeout(() => finish(failure('Script execution timed out after 500ms')), 500);
    worker.onmessage = event => finish(event.data);
    worker.onerror = event => {
      event.preventDefault();
      finish(failure(event.message.replace(/^Uncaught (?:\w*Error: )?/, '')));
    };
  } catch(e) { finish(failure(e.message)); }
}))`

type browserVerifier struct {
	browser *testbrowser.Browser
	page    *testbrowser.Page
}

func startVerifier(ctx context.Context) (*browserVerifier, error) {
	browser, err := testbrowser.Start(ctx, testbrowser.Options{})
	if err != nil {
		return nil, err
	}
	// No external page, credentials, application configuration or user profile.
	// The Blob worker inherits this policy, including no network or eval/wasm.
	html := `<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; worker-src blob:; connect-src 'none'">`
	page, err := browser.NewPage(ctx, "data:text/html,"+url.PathEscape(html))
	if err != nil {
		return nil, errors.Join(err, browser.Close())
	}
	return &browserVerifier{browser, page}, nil
}

func (v *browserVerifier) verify(ctx context.Context, f fixture, text string) (verification, error) {
	payload, err := jsonBytes(message{"fn": f.Fn, "tests": f.Tests, "text": text}, false)
	if err != nil {
		return verification{}, err
	}
	// Bound CDP/infrastructure failures separately from the 500ms Worker watchdog.
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result verification
	if err := v.page.Eval(bounded, verifyExpression+"("+string(payload)+")", &result); err != nil {
		return result, fmt.Errorf("fixture browser verification: %w", err)
	}
	if result.Total != len(f.Tests) || result.Passed < 0 || result.Passed > result.Total || result.Success != (result.Passed == result.Total) {
		return result, errors.New("fixture browser returned invalid verification counters")
	}
	return result, nil
}

func execute(ctx context.Context, root string, args []string, stdout io.Writer) (err error) {
	cfg, err := parseArgs(root, args)
	if err != nil {
		return err
	}
	r := runner{cfg: cfg, probe: commandProbe(root), stdout: stdout}
	if len(cfg.models) > 0 && len(cfg.fixtures) > 0 {
		verifier, err := startVerifier(ctx)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, verifier.browser.Close()) }()
		r.verify = verifier.verify
	}
	return r.run(ctx)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	root, err := repositoryRoot()
	if err == nil {
		err = execute(ctx, root, os.Args[1:], os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "reasoning-history:", err)
		os.Exit(1)
	}
}
