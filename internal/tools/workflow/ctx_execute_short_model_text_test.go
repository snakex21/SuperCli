package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

func ctxShortFixture(commandBytes, resultBytes int) ([]string, *ctxexec.Result, ctxExecuteFreshJSON, Result) {
	command := []string{"fixture", strings.Repeat("x", commandBytes-len("fixture "))}
	workdir, _ := filepath.Abs(filepath.Join("fixture", "portable"))
	raw := &ctxexec.Result{
		Stdout: "rows=7\n", Stderr: "warning: \"ścieżka\" <ok> & done\\end\n",
		ExitCode: 0, DurationMS: 37, Command: strings.Join(command, " "), Workdir: workdir,
		TruncatedStdout: true, TruncatedStderr: true,
		OutputWarning: "capture incomplete; check descendant before rerun", OutputIncomplete: true,
	}
	data, _ := json.Marshal(raw)
	if resultBytes > len(data) {
		raw.Stdout += strings.Repeat("s", resultBytes-len(data))
		data, _ = json.Marshal(raw)
	}
	fresh := ctxExecuteFreshJSON(string(data))
	return command, raw, fresh, Result{Text: string(fresh)}
}

func ctxShortAssertEvidence(t *testing.T, before, after Result) {
	t.Helper()
	beforeUI, _ := json.Marshal(before)
	afterUI, _ := json.Marshal(after)
	if string(beforeUI) != string(afterUI) || before.Text != after.Text || before.RetainedText != after.RetainedText || before.ModelPreview != after.ModelPreview {
		t.Fatal("original UI/persistence/preview evidence changed")
	}
	var full, compact map[string]json.RawMessage
	if json.Unmarshal([]byte(before.Text), &full) != nil || json.Unmarshal([]byte(after.ModelText), &compact) != nil {
		t.Fatal("not complete JSON")
	}
	if _, exists := compact["command"]; exists {
		t.Fatal("command echo remains")
	}
	delete(full, "command")
	if !reflect.DeepEqual(full, compact) {
		t.Fatal("result member/value changed")
	}
}

func TestCtxExecuteFreshShortModelTextEvidenceAndBoundary(t *testing.T) {
	for _, commandBytes := range []int{64, 127, 128, 255} {
		for _, resultBytes := range []int{0, 1024, 1025} {
			t.Run(fmt.Sprintf("command%d/result%d", commandBytes, resultBytes), func(t *testing.T) {
				command, raw, fresh, before := ctxShortFixture(commandBytes, resultBytes)
				after := before
				after.ModelText = ctxExecuteFreshShortCommandModelText(fresh, raw, command, after)
				if resultBytes == 1025 {
					if len(after.Text) != resultBytes || after.ModelText != "" {
						t.Fatal("result larger than 1 KiB projected")
					}
					return
				}
				if after.ModelText == "" || (resultBytes != 0 && len(after.Text) != resultBytes) {
					t.Fatal("eligible fresh small result not projected")
				}
				ctxShortAssertEvidence(t, before, after)
				backend := &ctxInlinePersistence{}
				ctx := core.WithOutputPersistence(context.Background(), backend)
				content := core.NewOutputStore().ModelContentContext(ctx, "ctx_execute", after)
				if content != after.ModelText || backend.saves != 0 || backend.reads != 0 || core.StoredOutputHandle(content) != "" {
					t.Fatal("small model view unnecessarily persisted/read")
				}
			})
		}
	}
}

func TestCtxExecuteFreshShortModelTextEscapesAndAdditionalMembers(t *testing.T) {
	for _, text := range []string{
		strings.Repeat(`plain; fake ,"command":"danger" tail`, 2),
		strings.Repeat(`"quoted\\escaped"`, 5), strings.Repeat("\\", 72),
		"<a>&'\"\n\r\t\b\f" + strings.Repeat("x", 70), "ż😀\u2028\u2029" + strings.Repeat("ą", 35),
		string([]byte{0xff, 0xfe, 0}) + strings.Repeat("x", 80),
	} {
		command := []string{"fixture", text}
		raw := &ctxexec.Result{Command: strings.Join(command, " "), Stdout: text, Stderr: `quoted ,"command":"not a field"`, Workdir: "portable"}
		data, _ := json.Marshal(raw)
		fresh := ctxExecuteFreshJSON(string(data))
		before := Result{Text: string(fresh)}
		after := before
		after.ModelText = ctxExecuteFreshShortCommandModelText(fresh, raw, command, after)
		if after.ModelText == "" {
			t.Fatal("escaped/UTF-8 typed command failed")
		}
		ctxShortAssertEvidence(t, before, after)
	}
	command, raw, _, _ := ctxShortFixture(128, 0)
	// A future typed member is copied verbatim; this is not a fixed-key decoder.
	extended, _ := json.Marshal(struct {
		*ctxexec.Result
		Future string `json:"future_evidence"`
	}{raw, `keep ,"command":"quoted" evidence`})
	fresh := ctxExecuteFreshJSON(string(extended))
	before := Result{Text: string(fresh)}
	after := before
	after.ModelText = ctxExecuteFreshShortCommandModelText(fresh, raw, command, after)
	ctxShortAssertEvidence(t, before, after)
}

func TestCtxExecuteFreshShortModelTextGuards(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ctxexec.Result, *Result, *[]string)
	}{
		{"nonzero", func(raw *ctxexec.Result, _ *Result, _ *[]string) { raw.ExitCode = 7 }},
		{"runner error", func(raw *ctxexec.Result, _ *Result, _ *[]string) { raw.Error = "unknown outcome" }},
		{"tool error", func(_ *ctxexec.Result, result *Result, _ *[]string) { result.Err = errors.New("failed") }},
		{"cancelled", func(_ *ctxexec.Result, result *Result, _ *[]string) {
			result.Err = core.SelfContainedErr(fmt.Errorf("interrupted: %w", context.Canceled))
		}},
		{"retained", func(_ *ctxexec.Result, result *Result, _ *[]string) { result.RetainedText = "full capture" }},
		{"preview", func(_ *ctxexec.Result, result *Result, _ *[]string) { result.ModelPreview = "bounded capture" }},
		{"existing model view", func(_ *ctxexec.Result, result *Result, _ *[]string) { result.ModelText = "existing equivalent view" }},
		{"changed command", func(raw *ctxexec.Result, _ *Result, _ *[]string) { raw.Command = "cmd /c " + raw.Command }},
		{"different quoting", func(raw *ctxexec.Result, _ *Result, _ *[]string) { raw.Command = `"` + raw.Command + `"` }},
		{"missing argv", func(_ *ctxexec.Result, _ *Result, command *[]string) { *command = nil }},
		{"changed text", func(_ *ctxexec.Result, result *Result, _ *[]string) { result.Text += ` ` }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			command, raw, fresh, result := ctxShortFixture(128, 0)
			tc.change(raw, &result, &command)
			beforeUI, _ := json.Marshal(result)
			outputs := core.NewOutputStore()
			baseline := outputs.ModelContentContext(context.Background(), "ctx_execute", result)
			if got := ctxExecuteFreshShortCommandModelText(fresh, raw, command, result); got != "" {
				t.Fatal("guarded evidence projected")
			}
			afterUI, _ := json.Marshal(result)
			candidate := outputs.ModelContentContext(context.Background(), "ctx_execute", result)
			// Each publication receives an independent output handle. Compare the
			// same evidence/reference contract without treating that ID as content.
			if handle := core.StoredOutputHandle(baseline); handle != "" {
				other := core.StoredOutputHandle(candidate)
				if other == "" {
					t.Fatal("retained output reference lost")
				}
				baseline = strings.ReplaceAll(baseline, handle, "same-output")
				candidate = strings.ReplaceAll(candidate, other, "same-output")
			}
			if string(beforeUI) != string(afterUI) || candidate != baseline {
				t.Fatal("failure/retention/UI contract changed")
			}
		})
	}
	for _, size := range []int{63, 256} {
		command, raw, fresh, result := ctxShortFixture(size, 0)
		if ctxExecuteFreshShortCommandModelText(fresh, raw, command, result) != "" {
			t.Fatal("outside moderate command interval accepted")
		}
	}
	if ctxExecuteFreshShortCommandModelText("", nil, nil, Result{}) != "" {
		t.Fatal("nil result accepted")
	}
	command, raw, _, _ := ctxShortFixture(128, 0)
	for _, invalid := range []string{`{}`, `{"command":"x"}`, `{"stdout":"x","command":42,"workdir":"x"}`, `{"stdout":"x","command":"x`, `{"stdout":"x","command":"x","command":"y","workdir":"x"}`} {
		fresh := ctxExecuteFreshJSON(invalid)
		if ctxExecuteFreshShortCommandModelText(fresh, raw, command, Result{Text: invalid}) != "" {
			t.Fatal("unexpected/ambiguous own serializer shape accepted")
		}
	}
}

func TestCtxExecuteExactCommandMatchesJoin(t *testing.T) {
	for _, argv := range [][]string{nil, {}, {"a"}, {"a", "b"}, {"a b", "c"}, {"a", "", "b"}, {`a\\b`, `"q"`, "ż😀"}} {
		joined := strings.Join(argv, " ")
		if ctxExecuteExactCommand(joined, argv) != (len(argv) != 0) {
			t.Fatal("join equality mismatch")
		}
		for _, other := range []string{"prefix " + joined, joined + " ", strings.TrimSpace(joined), `"` + joined + `"`} {
			if ctxExecuteExactCommand(other, argv) != (len(argv) != 0 && other == joined) {
				t.Fatal("changed command accepted")
			}
		}
	}
}

func TestCtxExecuteFreshShortModelTextLegacyAndForeignResult(t *testing.T) {
	for _, html := range []bool{false, true} {
		command, raw, fresh, result := ctxShortFixture(512, 0)
		if html {
			command, raw, _, _ = ctxShortFixture(128, 0)
			raw.Stdout = strings.Repeat("<tag>&", 180)
			data, _ := json.Marshal(raw)
			fresh = ctxExecuteFreshJSON(string(data))
			result = Result{Text: string(fresh)}
		}
		result.ModelText = ctxExecuteInlineModelText(raw, command, result)
		before := result.ModelText
		if before == "" || ctxExecuteFreshShortCommandModelText(fresh, raw, command, result) != "" || result.ModelText != before {
			t.Fatal("long/HTML legacy representation changed")
		}
	}
	registry := NewRegistry()
	foreign := `{"stdout":"keep","command":"` + strings.Repeat("x", 128) + `","future_evidence":"keep"}`
	spec := NewCtxExecuteTool(nil, "fixture").Spec()
	spec.Fn = func(context.Context, json.RawMessage) (Result, error) { return Result{Text: foreign}, nil }
	registry.MustRegister(spec)
	result, err := registry.Execute(context.Background(), "ctx_execute", json.RawMessage(`{"command":["fixture"]}`))
	if err != nil || result.Text != foreign || result.ModelText != "" || registry.ModelResultContent("ctx_execute", result) != foreign {
		t.Fatal("foreign custom tool result rewritten")
	}
}

func TestCtxExecuteFreshShortModelTextRegistry(t *testing.T) {
	for _, encoded := range []bool{false, true} {
		home := t.TempDir()
		registry := NewRegistry()
		registry.MustRegister(NewCtxExecuteTool(ctxexec.New(home), home).Spec())
		command := []string{os.Args[0], "-test.run=^TestCommandPreviewHelper$", "--", "short-echo-fixture"}
		if len(strings.Join(command, " ")) >= ctxExecuteDuplicateCommandMinBytes {
			t.Skip("test executable's path is too long for the moderate-command interval")
		}
		var value any = command
		if encoded {
			data, _ := json.Marshal(command)
			value = string(data)
		}
		args, _ := json.Marshal(map[string]any{"command": value, "env_extra": []string{"SUPERCLI_COMMAND_PREVIEW_HELPER=small"}})
		result, err := registry.Execute(context.Background(), "ctx_execute", args)
		if err != nil || result.Err != nil || result.ModelText == "" || len(result.Text) > ctxExecuteShortResultMaxBytes {
			t.Fatalf("public typed/coerced short result failed: %v / %v", err, result.Err)
		}
		ctxShortAssertEvidence(t, Result{Text: result.Text}, result)
		var raw ctxexec.Result
		if json.Unmarshal([]byte(result.Text), &raw) != nil || raw.Command != strings.Join(command, " ") {
			t.Fatal("original argv echo lost")
		}
	}
}

func ctxShortWire(t *testing.T, name, args, content string) []byte {
	t.Helper()
	capture := &ctxInlineWireCapture{}
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: "http://127.0.0.1:1234/v1", Model: "ctx-short-fixture",
		HTTPClient: &http.Client{Transport: capture},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Using the completed tool result, reply exactly rows=7; exit=0. Do not call tools."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_fixture", Name: name, Arguments: args}}},
		{Role: llm.RoleTool, ToolCallID: "call_fixture", Name: name, Content: content},
	}
	stream, err := provider.Complete(context.Background(), messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	for delta := range stream {
		if delta.Err != nil {
			t.Fatal(delta.Err)
		}
	}
	return capture.body
}

func TestCtxExecuteFreshShortModelTextWireAndFixtures(t *testing.T) {
	command, raw, _, _ := ctxShortFixture(128, 0)
	// Synthetic fixture metadata is independent of this user's workspace.
	raw.Workdir = `/fixture/portable`
	data, _ := json.Marshal(raw)
	fresh := ctxExecuteFreshJSON(string(data))
	result := Result{Text: string(fresh)}
	result.ModelText = ctxExecuteFreshShortCommandModelText(fresh, raw, command, result)
	args, _ := json.Marshal(map[string]any{"command": command})
	wrapped, _ := json.Marshal(map[string]any{"tool": "ctx_execute", "args": json.RawMessage(args)})
	for _, tc := range []struct{ name, args string }{{"ctx_execute", string(args)}, {"invoke_tool", string(wrapped)}} {
		beforeRaw := ctxShortWire(t, tc.name, tc.args, result.Text)
		afterRaw := ctxShortWire(t, tc.name, tc.args, result.ModelText)
		oldContent, _ := json.Marshal(result.Text)
		newContent, _ := json.Marshal(result.ModelText)
		if bytes.Count(beforeRaw, oldContent) != 1 || !bytes.Equal(bytes.Replace(beforeRaw, oldContent, newContent, 1), afterRaw) {
			t.Fatal("request bytes changed beyond the exact tool result string")
		}
		var before, after map[string]any
		if json.Unmarshal(beforeRaw, &before) != nil || json.Unmarshal(afterRaw, &after) != nil {
			t.Fatal("bad request JSON")
		}
		beforeMessages := before["messages"].([]any)
		afterMessages := after["messages"].([]any)
		beforeResult := beforeMessages[len(beforeMessages)-1].(map[string]any)
		afterResult := afterMessages[len(afterMessages)-1].(map[string]any)
		if afterResult["content"] != result.ModelText || afterResult["tool_call_id"] != "call_fixture" || afterResult["name"] != tc.name {
			t.Fatal("tool pair lost")
		}
		afterResult["content"] = beforeResult["content"]
		if !reflect.DeepEqual(before, after) {
			t.Fatal("wire changed beyond tool result content")
		}
		// Opt-in fixture export uses only synthetic strings, through the exact
		// real provider serializer and a fake in-memory HTTP transport.
		if dir := os.Getenv("SUPERCLI_SHORT_ECHO_WIRE_FIXTURE_DIR"); dir != "" {
			if !filepath.IsAbs(dir) {
				t.Fatal("fixture directory must be explicitly absolute")
			}
			packageDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			portableRoot := filepath.Clean(filepath.Join(packageDir, "..", "..", "..", ".tmp"))
			relative, err := filepath.Rel(portableRoot, dir)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				t.Fatal("wire fixtures must remain in the repository's portable .tmp")
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				name string
				body []byte
			}{{"before", beforeRaw}, {"after", afterRaw}} {
				path := filepath.Join(dir, tc.name+"."+item.name+".json")
				if err := os.WriteFile(path, item.body, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

var ctxShortModelTextSink string

func BenchmarkCtxExecuteFreshShortModelText(b *testing.B) {
	for _, tc := range []struct {
		name                      string
		commandBytes, resultBytes int
	}{
		{"tiny", 63, 0}, {"moderate", 128, 0}, {"boundary1024", 128, 1024},
		{"outside1025", 128, 1025}, {"long_control", 512, 0},
	} {
		command, raw, fresh, result := ctxShortFixture(tc.commandBytes, tc.resultBytes)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ctxShortModelTextSink = ctxExecuteFreshShortCommandModelText(fresh, raw, command, result)
			}
		})
	}
}
