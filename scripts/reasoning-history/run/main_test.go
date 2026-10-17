package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const escapedCorrect = `function splitEscaped(s) {
  const out = []; let field = '';
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && i + 1 < s.length) field += s[++i];
    else if (s[i] === ',') { out.push(field); field = ''; }
    else field += s[i];
  }
  out.push(field); return out;
}`

const cacheCorrect = `function runCache(events, ttl) {
  const cache = new Map(), out = [];
  for (const e of events) {
    if (e.op === 'put') cache.set(e.key, {value:e.value, expires:e.t+ttl});
    else if (e.op === 'delete') cache.delete(e.key);
    else if (e.op === 'get') {
      const item = cache.get(e.key);
      out.push(item && e.t < item.expires ? item.value : null);
    }
  }
  return out;
}`

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestArgumentCompatibility(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".tmp", "history-eval")
	for _, tc := range []struct {
		name   string
		args   []string
		out    string
		models int
		tasks  int
	}{
		{"defaults", nil, "experiment", 4, 2},
		{"empty defaults", []string{"", "", ""}, "experiment", 4, 2},
		{"filters", []string{"nested/run", models[2].Model + "," + models[0].Model, "cache-expiry"}, "nested/run", 2, 1},
		{"unknown model", []string{"x", "unknown"}, "x", 0, 2},
		{"unknown fixture", []string{"x", "", "unknown"}, "x", 4, 0},
		{"no filter trimming", []string{"x", " " + models[0].Model}, "x", 0, 2},
		{"output root allowed", []string{"."}, ".", 4, 2},
		{"absolute inside allowed", []string{filepath.Join(base, "inside")}, "inside", 4, 2},
		{"extra positional ignored", []string{"x", "", "", "ignored"}, "x", 4, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseArgs(root, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.outdir != filepath.Join(base, tc.out) || len(cfg.models) != tc.models || len(cfg.fixtures) != tc.tasks {
				t.Fatalf("unexpected config: %+v", cfg)
			}
			if tc.name == "filters" && cfg.models[0].Model != models[0].Model {
				t.Fatal("model filters must retain catalog order")
			}
		})
	}
	for _, bad := range []string{"..", "../outside", filepath.Join(base+"-sibling", "bad"), filepath.Join(root, "elsewhere")} {
		if _, err := parseArgs(root, []string{bad}); err == nil || err.Error() != "Output must remain under .tmp/history-eval" {
			t.Fatalf("escaping output %q was accepted: %v", bad, err)
		}
	}
}

func TestFixtureInventory(t *testing.T) {
	fs := fixtures()
	if len(fs) != 2 || fs[0].ID != "escaped-fields" || fs[0].Fn != "splitEscaped" || fs[0].File != "parser.js" || len(fs[0].Tests) != 9 || fs[1].ID != "cache-expiry" || len(fs[1].Tests) != 7 {
		t.Fatalf("fixture contract changed: %+v", fs)
	}
	if fs[0].Tests[8].Args[0] != "a\\\\\\,b,c" {
		t.Fatalf("three-backslash fixture corrupted: %q", fs[0].Tests[8].Args[0])
	}
}

func TestSSEParsingAndNativeReply(t *testing.T) {
	stream := ": heartbeat\r\nevent: response.output_item.done\r\ndata: {\r\ndata:\t\"type\":\"response.output_item.done\",\r\ndata: \"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"opaque\"}}\r\n\r\n" +
		"data: not JSON\n\ndata: [DONE]\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"fallback\"}]}}"
	got := events(stream)
	if len(got) != 3 {
		t.Fatalf("expected 3 JSON events, got %+v", got)
	}
	messages, err := reply(models[2], probeResult{SSE: stream})
	if err != nil || len(messages) != 2 || messages[0]["encrypted_content"] != "opaque" || messages[1]["type"] != "message" {
		t.Fatalf("native done items lost or replaced by completed: %+v, %v", messages, err)
	}
	fallback := "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"first\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"second\"}]}}\n\n"
	messages, err = reply(models[2], probeResult{SSE: fallback})
	if err != nil || len(messages) != 1 || messages[0]["id"] != "first" {
		t.Fatalf("first completed response fallback changed: %+v, %v", messages, err)
	}
	if _, err := reply(models[2], probeResult{SSE: "data: [DONE]\n\n"}); err == nil || err.Error() != "Missing native Responses output" {
		t.Fatalf("missing native output accepted: %v", err)
	}
}

func TestChatReply(t *testing.T) {
	messages, err := reply(models[0], probeResult{Text: "visible", Reasoning: "native", Calls: []toolCall{{"c1", "read", `{"filePath":"parser.js"}`}}})
	if err != nil || len(messages) != 1 {
		t.Fatal(messages, err)
	}
	data := string(rawJSON(t, messages))
	for _, want := range []string{`"reasoning_content":"native"`, `"role":"assistant"`, `"tool_calls":[`, `"type":"function"`, `"id":"c1"`, `"content":"visible"`} {
		if !strings.Contains(data, want) {
			t.Fatalf("chat reply missing %s: %s", want, data)
		}
	}
	messages, _ = reply(models[0], probeResult{})
	if _, ok := messages[0]["reasoning_content"]; ok {
		t.Fatal("empty reasoning must be omitted")
	}
	if _, ok := messages[0]["tool_calls"]; ok {
		t.Fatal("empty tool calls must be omitted")
	}
}

func TestStripOnlyCompletedHistory(t *testing.T) {
	history := []message{
		{"type": "reasoning", "encrypted_content": "opaque"},
		{"role": "assistant", "reasoning_content": "native", "reasoning_details": []any{"detail"}, "content": "\ufeff <THINK>secret</THINK> visible <reflection>also secret</reflection> "},
		{"role": "assistant", "content": []any{
			message{"type": "thinking", "text": "secret"}, message{"type": "redacted_thinking"},
			message{"type": "text", "text": "<thinking>secret</thinking> answer", "other": "kept"},
			message{"type": "image", "data": "unchanged"},
		}},
		{"role": "user", "content": "<think>keep user text</think>"},
		{"type": "function_call", "call_id": "id", "arguments": "{}"},
	}
	before := string(rawJSON(t, history))
	got := stripCompleted(history)
	if len(got) != 4 || got[0]["content"] != "visible" || got[0]["reasoning_content"] != nil || got[0]["reasoning_details"] != nil {
		t.Fatalf("completed reasoning not removed: %+v", got)
	}
	parts := got[1]["content"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["text"] != "answer" || parts[0].(map[string]any)["other"] != "kept" || got[2]["content"] != "<think>keep user text</think>" || got[3]["type"] != "function_call" {
		t.Fatalf("non-reasoning history changed: %+v", got)
	}
	if string(rawJSON(t, history)) != before {
		t.Fatal("DROP mutated the shared planning history")
	}
	unpaired := stripCompleted([]message{{"role": "assistant", "content": "<think>keep</thinking>"}})
	if unpaired[0]["content"] != "<think>keep</thinking>" {
		t.Fatal("mismatched tags must not be stripped")
	}
}

func testRunner(t *testing.T, probe probeFunc) *runner {
	t.Helper()
	cfg, err := parseArgs(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.outdir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &runner{cfg: cfg, probe: probe, stdout: io.Discard}
}

func TestTurnMetricsAndToolFeedback(t *testing.T) {
	for _, m := range []model{models[0], models[2]} {
		t.Run(m.Transport, func(t *testing.T) {
			f := fixtures()[0]
			step := 0
			var inputs []probeInput
			r := testRunner(t, func(_ context.Context, in probeInput) (json.RawMessage, error) {
				in.History = cloneHistory(in.History)
				inputs = append(inputs, in)
				step++
				if step == 1 {
					return rawJSON(t, message{"Text": "", "Reasoning": "🧠", "Calls": []toolCall{
						{"ok", "read", `{"filePath":"parser.js"}`}, {"wrong", "read", `{"filePath":"other.js"}`},
						{"invalid", "read", `{broken`}, {"bash", "bash", `{"command":"never execute"}`},
					}, "Usage": message{"Input": 10, "Output": 4, "Reasoning": 2, "CachedInput": 3}, "ElapsedMS": 70, "TTFTMS": 11,
						"SSE": "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"current\"}}\n\n"}), nil
				}
				return rawJSON(t, message{"Text": "final", "Reasoning": "z", "Usage": message{"Input": 20, "Output": 6, "Reasoning": 1, "CachedInput": 2}, "ElapsedMS": 50, "TTFTMS": 99,
					"SSE": "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\"}]}}\n\n"}), nil
			})
			history := []message{{"role": "user", "content": "fixture"}}
			result, err := r.turn(t.Context(), m, &history, "session", f, "test")
			want := stats{Calls: 2, ToolCalls: 4, Input: 30, Output: 10, Reasoning: 3, Cached: 5, MS: 120, TTFT: 11, ReasoningChars: 3, Errors: []string{}}
			if err != nil || result.Text != "final" || !reflect.DeepEqual(result.Stats, want) {
				t.Fatalf("metrics changed: %+v, %v; want %+v", result, err, want)
			}
			feedback := inputs[1].History
			if len(feedback) != 6 || inputs[1].Effort != "low" || inputs[1].Session != "session" {
				t.Fatalf("probe contract changed: %+v", inputs[1])
			}
			key := "content"
			if m.Transport == "responses" {
				key = "output"
				if feedback[2]["type"] != "function_call_output" || feedback[2]["call_id"] != "ok" || feedback[1]["encrypted_content"] != "current" {
					t.Fatalf("native current-turn history lost: %+v", feedback)
				}
			} else if feedback[2]["role"] != "tool" || feedback[2]["tool_call_id"] != "ok" || feedback[1]["reasoning_content"] != "🧠" {
				t.Fatalf("chat current-turn history lost: %+v", feedback)
			}
			if feedback[2][key] != f.Source || feedback[3][key] != "Unknown fixture path; available: parser.js" || feedback[4][key] != feedback[3][key] || !strings.HasPrefix(feedback[5][key].(string), "No shell is available.") {
				t.Fatalf("synthetic tool feedback changed: %+v", feedback)
			}
		})
	}
}

func TestTurnFailureBehavior(t *testing.T) {
	t.Run("provider error", func(t *testing.T) {
		r := testRunner(t, func(context.Context, probeInput) (json.RawMessage, error) {
			return rawJSON(t, message{"Error": "provider failed", "Text": "partial", "ElapsedMS": 5, "TTFTMS": 0}), nil
		})
		history := []message{{"role": "user", "content": "fixture"}}
		got, err := r.turn(t.Context(), models[0], &history, "s", fixtures()[0], "error")
		if err != nil || got.Text != "partial" || got.Stats.Calls != 1 || len(history) != 1 || !reflect.DeepEqual(got.Stats.Errors, []string{"provider failed"}) {
			t.Fatalf("provider failure changed: %+v, %v", got, err)
		}
	})
	t.Run("four tool steps", func(t *testing.T) {
		r := testRunner(t, func(context.Context, probeInput) (json.RawMessage, error) {
			return rawJSON(t, message{"Text": "last", "Calls": []toolCall{{"c", "bash", `{}`}}}), nil
		})
		history := []message{}
		got, err := r.turn(t.Context(), models[0], &history, "s", fixtures()[0], "limit")
		if err != nil || got.Text != "last" || got.Stats.Calls != 4 || got.Stats.ToolCalls != 4 || !reflect.DeepEqual(got.Stats.Errors, []string{"tool step limit"}) {
			t.Fatalf("step limit changed: %+v, %v", got, err)
		}
	})
	t.Run("null arguments retain fatal error", func(t *testing.T) {
		r := testRunner(t, func(context.Context, probeInput) (json.RawMessage, error) {
			return rawJSON(t, message{"Calls": []toolCall{{"c", "read", `null`}}}), nil
		})
		history := []message{}
		if _, err := r.turn(t.Context(), models[0], &history, "s", fixtures()[0], "null"); err == nil || !strings.Contains(err.Error(), "Cannot read properties of null") {
			t.Fatal(err)
		}
	})
	t.Run("probe infrastructure error", func(t *testing.T) {
		r := testRunner(t, func(context.Context, probeInput) (json.RawMessage, error) { return nil, errors.New("spawn failed") })
		history := []message{}
		if _, err := r.turn(t.Context(), models[0], &history, "s", fixtures()[0], "spawn"); err == nil || err.Error() != "spawn failed" || r.seq != 0 {
			t.Fatal(err)
		}
	})
}

// A subprocess fixture exercises the actual stdin/stdout probe boundary. Its
// sole network peer is this test's loopback mock, never an endpoint in models.
func TestMain(m *testing.M) {
	if os.Getenv("SUPERCLI_HISTORY_PROBE_HELPER") == "1" {
		switch os.Getenv("SUPERCLI_HISTORY_PROBE_MODE") {
		case "exit":
			fmt.Fprint(os.Stderr, "fixture stderr")
			os.Exit(9)
		case "invalid":
			fmt.Fprint(os.Stdout, "not JSON")
			fmt.Fprint(os.Stderr, "fixture stderr")
			os.Exit(0)
		}
		request, err := http.NewRequest(http.MethodPost, os.Getenv("SUPERCLI_HISTORY_MOCK_URL"), os.Stdin)
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
			var response *http.Response
			response, err = (&http.Client{Timeout: 10 * time.Second}).Do(request)
			if err == nil {
				_, err = io.Copy(os.Stdout, response.Body)
				response.Body.Close()
			}
		}
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func mockProbe(address, mode string) probeFunc {
	return processProbe(func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "SUPERCLI_HISTORY_PROBE_HELPER=1", "SUPERCLI_HISTORY_MOCK_URL="+address, "SUPERCLI_HISTORY_PROBE_MODE="+mode)
		return cmd
	})
}

func TestProbeProcessFailures(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{{"exit", "probe exit 9: fixture stderr"}, {"invalid", "not JSONfixture stderr"}} {
		t.Run(tc.mode, func(t *testing.T) {
			if _, err := mockProbe("", tc.mode)(t.Context(), probeInput{}); err == nil || err.Error() != tc.want {
				t.Fatalf("probe failure contract changed: %v", err)
			}
		})
	}
}

func startTestVerifier(t *testing.T) *browserVerifier {
	t.Helper()
	v, err := startVerifier(t.Context())
	if err != nil {
		t.Fatal(err) // Do not silently skip the native-JS verification contract.
	}
	t.Cleanup(func() {
		if err := v.browser.Close(); err != nil {
			t.Error(err)
		}
	})
	return v
}

func TestNativeWorkerVerification(t *testing.T) {
	v := startTestVerifier(t)
	fs := fixtures()
	for _, tc := range []struct {
		name   string
		f      fixture
		code   string
		passed int
		err    string
	}{
		{"escaped correct", fs[0], escapedCorrect, 9, ""},
		{"cache correct", fs[1], cacheCorrect, 7, ""},
		{"model top-level names", fs[0], "const got = 1, wants = 2;\n" + escapedCorrect, 9, ""},
		{"javascript fence", fs[0], "\n```JavaScript\n" + escapedCorrect + "\n```\n", 9, ""},
		{"js CRLF fence", fs[1], "```js\r\n" + cacheCorrect + "\r\n```", 7, ""},
		{"escaped original bug", fs[0], fs[0].Source, 4, ""},
		{"cache original bug", fs[1], fs[1].Source, 3, ""},
		{"runtime error", fs[0], `function splitEscaped() { throw new Error('fixture failure'); }`, 0, "fixture failure"},
		{"syntax error", fs[0], `function splitEscaped( {`, 0, "syntax"},
		{"infinite loop", fs[0], `function splitEscaped() { while (true) {} }`, 0, "Script execution timed out after 500ms"},
		{"page survives infinite loop", fs[0], escapedCorrect, 9, ""},
		{"worker writes isolated state", fs[0], "self.fixtureLeak = 1;\n" + escapedCorrect, 9, ""},
		{"next worker is fresh", fs[0], "if (self.fixtureLeak) throw new Error('leaked state');\n" + escapedCorrect, 9, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			got, err := v.verify(t.Context(), tc.f, tc.code)
			if err != nil {
				t.Fatal(err)
			}
			if got.Passed != tc.passed || got.Total != len(tc.f.Tests) || got.Success != (tc.passed == len(tc.f.Tests)) {
				t.Fatalf("verification counters changed: %+v", got)
			}
			if tc.err != "" {
				if got.Error == nil || *got.Error == "" || len(got.Got) != 0 {
					t.Fatalf("missing JS error: %+v", got)
				}
				if tc.err != "syntax" && *got.Error != tc.err {
					t.Fatalf("error = %q, want %q", *got.Error, tc.err)
				}
			} else if got.Error != nil || len(got.Got) == 0 {
				t.Fatalf("missing computed JS output: %+v", got)
			}
			if tc.name == "infinite loop" && (time.Since(start) < 450*time.Millisecond || time.Since(start) > 5*time.Second) {
				t.Fatalf("500ms worker watchdog did not bound execution: %s", time.Since(start))
			}
		})
	}
	var leaked string
	if err := v.page.Eval(t.Context(), "typeof fixtureLeak", &leaked); err != nil || leaked != "undefined" {
		t.Fatalf("model code reached main-page globals: %q, %v", leaked, err)
	}
}

func TestExperimentWithMockHTTPAndNativeJS(t *testing.T) {
	v := startTestVerifier(t)
	for _, m := range []model{models[0], models[2]} {
		t.Run(m.Transport, func(t *testing.T) {
			var mu sync.Mutex
			var requests []probeInput
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var in probeInput
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				requests = append(requests, in)
				mu.Unlock()
				f := fixtures()[0]
				if strings.Contains(in.Session, "cache-expiry") {
					f = fixtures()[1]
				}
				implement := false
				for _, item := range in.History {
					if text, _ := item["content"].(string); item["role"] == "user" && strings.HasPrefix(text, "Implement that correction now.") {
						implement = true
					}
				}
				last := in.History[len(in.History)-1]
				toolReply := last["role"] == "tool" || last["type"] == "function_call_output"
				text := "Small correction explained."
				reasoning := "historical🧠"
				if implement {
					reasoning = "current🧠"
					text = escapedCorrect
					if f.ID == "cache-expiry" {
						text = cacheCorrect
					}
				}
				calls := []toolCall{}
				items := []message{{"type": "reasoning", "encrypted_content": reasoning}}
				if !toolReply {
					text = ""
					calls = append(calls, toolCall{"read-call", "read", `{"filePath":"` + f.File + `"}`})
					items = append(items, message{"type": "function_call", "call_id": "read-call", "name": "read", "arguments": calls[0].Arguments})
				} else {
					items = append(items, message{"type": "message", "role": "assistant", "content": []any{message{"type": "output_text", "text": text}}})
				}
				var sse strings.Builder
				for _, item := range items {
					fmt.Fprintf(&sse, "data: %s\r\n\r\n", rawJSON(t, message{"type": "response.output_item.done", "item": item}))
				}
				result := rawJSON(t, message{"Text": text, "Reasoning": reasoning, "Calls": calls, "Usage": message{"Input": 10, "Output": 3, "Reasoning": 1, "CachedInput": 2}, "ElapsedMS": 7, "TTFTMS": 2, "RequestBytes": 123, "SSE": sse.String(), "FutureField": "preserved"})
				// Chunk the local HTTP response to exercise subprocess stdout
				// accumulation as well as multiline native SSE preservation.
				w.Header().Set("Content-Type", "application/json")
				for len(result) > 0 {
					n := min(31, len(result))
					_, _ = w.Write(result[:n])
					w.(http.Flusher).Flush()
					result = result[n:]
				}
			}))
			t.Cleanup(server.Close)
			cfg, err := parseArgs(t.TempDir(), []string{"mock", m.Model})
			if err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			r := runner{cfg: cfg, probe: mockProbe(server.URL, ""), verify: v.verify, stdout: &stdout}
			if err := r.run(t.Context()); err != nil {
				t.Fatal(err)
			}
			var results []struct {
				Model        string       `json:"model"`
				Fixture      string       `json:"fixture"`
				Arm          string       `json:"arm"`
				Plan         stats        `json:"plan"`
				Stats        stats        `json:"stats"`
				Verification verification `json:"verification"`
			}
			data, err := os.ReadFile(filepath.Join(cfg.outdir, "results.json"))
			if err != nil || json.Unmarshal(data, &results) != nil || len(results) != 4 {
				t.Fatalf("results contract changed: %s, %v", data, err)
			}
			for i, want := range []string{"keep", "drop", "drop", "keep"} {
				item := results[i]
				if item.Arm != want || item.Model != m.Model || !item.Verification.Success || item.Stats.Calls != 2 || item.Stats.ToolCalls != 1 || item.Stats.Input != 20 || item.Stats.Output != 6 || item.Stats.Reasoning != 2 || item.Stats.Cached != 4 || item.Stats.MS != 14 || item.Stats.TTFT != 2 || item.Plan.Calls != 2 {
					t.Fatalf("arm output/metrics changed: %+v", item)
				}
			}
			mu.Lock()
			seen := append([]probeInput(nil), requests...)
			mu.Unlock()
			if len(seen) != 12 || len(strings.Split(strings.TrimSpace(stdout.String()), "\n")) != 16 {
				t.Fatalf("expected 12 requests and 4 arm events, got %d requests: %s", len(seen), stdout.String())
			}
			role := "system"
			if m.Transport == "responses" {
				role = "developer"
			}
			for i, in := range seen {
				if in.Model != m.Model || in.Base != m.Base || in.Transport != m.Transport || in.Effort != "low" || in.History[0]["role"] != role || in.History[0]["content"] != systemPrompt {
					t.Fatalf("probe parameters/history changed: %+v", in)
				}
				if i < 6 && in.Session != "history-eval-mock-"+m.Model+"-escaped-fields" {
					t.Fatalf("session identity changed: %s", in.Session)
				}
			}
			keep := string(rawJSON(t, seen[2].History))
			drop := string(rawJSON(t, seen[4].History))
			currentDrop := string(rawJSON(t, seen[5].History))
			if !strings.Contains(keep, "historical🧠") || strings.Contains(drop, "historical🧠") || !strings.Contains(currentDrop, "current🧠") {
				t.Fatalf("historical/current reasoning isolation changed: keep=%s drop=%s current=%s", keep, drop, currentDrop)
			}
			artifacts, _ := filepath.Glob(filepath.Join(cfg.outdir, "*.json"))
			if len(artifacts) != 13 {
				t.Fatalf("expected 12 numbered request archives and results.json, got %d", len(artifacts))
			}
			archive, err := os.ReadFile(filepath.Join(cfg.outdir, "001-plan-escaped-fields-0.json"))
			if err != nil || !strings.Contains(string(archive), `"FutureField": "preserved"`) || !strings.Contains(string(archive), `"RequestBytes": 123`) || !strings.Contains(string(archive), `\r\n\r\n`) {
				t.Fatalf("raw probe output/SSE lost: %s, %v", archive, err)
			}
		})
	}
}

func TestPlanErrorsAndPartialResults(t *testing.T) {
	calls := 0
	r := testRunner(t, func(context.Context, probeInput) (json.RawMessage, error) {
		calls++
		return rawJSON(t, message{"Error": "model unavailable", "Text": "partial"}), nil
	})
	r.cfg.models = models[:2]
	r.verify = func(context.Context, fixture, string) (verification, error) {
		t.Fatal("a failed plan must never run verification")
		return verification{}, nil
	}
	if err := r.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.cfg.outdir, "results.json"))
	var results []message
	if err != nil || json.Unmarshal(data, &results) != nil || len(results) != 2 || calls != 2 || results[0]["arm"] != "plan" || results[0]["verification"] != nil {
		t.Fatalf("plan errors must skip remaining fixtures for that model: %s, %v", data, err)
	}

	calls = 0
	r = testRunner(t, func(context.Context, probeInput) (json.RawMessage, error) {
		calls++
		if calls == 3 {
			return nil, errors.New("probe stopped")
		}
		return rawJSON(t, message{"Text": "fixture"}), nil
	})
	r.cfg.models = models[:1]
	r.cfg.fixtures = fixtures()[:1]
	r.verify = func(context.Context, fixture, string) (verification, error) {
		return verification{Passed: 9, Total: 9, Success: true, Got: json.RawMessage(`[]`)}, nil
	}
	if err := r.run(t.Context()); err == nil || err.Error() != "probe stopped" {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(r.cfg.outdir, "results.json"))
	if err != nil || json.Unmarshal(data, &results) != nil || len(results) != 1 || results[0]["arm"] != "keep" {
		t.Fatalf("completed arm checkpoint was lost on later fatal error: %s, %v", data, err)
	}
}

func TestEmptySelectionExecution(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"unknown-model", "not-in-catalog"}, {"unknown-fixture", "", "not-in-fixtures"}} {
		if err := execute(t.Context(), root, args, io.Discard); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(root, ".tmp", "history-eval", args[0], "results.json"))
		if err != nil || string(data) != "[]" {
			t.Fatalf("empty filters must write [] without browser/probe/model access: %s, %v", data, err)
		}
	}
}
