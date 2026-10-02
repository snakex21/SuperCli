package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func ctxInlineFixture() ([]string, *ctxexec.Result, Result) {
	command := []string{"powershell.exe", "-NoProfile", "-Command", "$names = @('Symbol0','Symbol1','Symbol2','Symbol3','Symbol4','Symbol5','Symbol6','Symbol7','Symbol8','Symbol9','Symbol10','Symbol11'); Get-ChildItem -Path src -Recurse -Filter *.go | ForEach-Object { $path = $_.FullName; $n = 0; [IO.File]::ReadLines($path) | ForEach-Object { $n++; foreach ($name in $names) { if ($_ -match ('\\b' + [regex]::Escape($name) + '\\b')) { [ordered]@{file=$path;line=$n;text=$_} | ConvertTo-Json -Compress; break } } } }"}
	workdir, _ := filepath.Abs(filepath.Join("fixture", "Example"))
	res := &ctxexec.Result{
		Stdout:   "{\"file\":\"src/handler.go\",\"line\":87,\"text\":\"Symbol11=7321\"}\n",
		Stderr:   "warning: ścieżka <example> & done\n",
		ExitCode: 0, DurationMS: 37, Command: strings.Join(command, " "),
		Workdir:         workdir,
		TruncatedStdout: true, OutputWarning: "capture incomplete; do not infer a failed launch",
		OutputIncomplete: true,
	}
	body, _ := json.Marshal(res)
	return command, res, Result{Text: string(body)}
}

type ctxInlinePersistence struct{ saves, reads int }

func (p *ctxInlinePersistence) SaveToolOutput(context.Context, string, string) error {
	p.saves++
	return nil
}
func (p *ctxInlinePersistence) ReadToolOutput(context.Context, string) (string, error) {
	p.reads++
	return "", errors.New("unexpected read")
}

func TestCtxExecuteInlineModelTextPreservesEvidence(t *testing.T) {
	command, res, result := ctxInlineFixture()
	original := result.Text
	result.ModelText = ctxExecuteInlineModelText(res, command, result)
	if result.ModelText == "" {
		t.Fatal("long duplicate command was not compacted")
	}
	var full, compact map[string]json.RawMessage
	if json.Unmarshal([]byte(original), &full) != nil || json.Unmarshal([]byte(result.ModelText), &compact) != nil {
		t.Fatal("result is not complete JSON")
	}
	if _, found := compact["command"]; found {
		t.Fatal("duplicate command remains")
	}
	delete(full, "command")
	if !reflect.DeepEqual(full, compact) || result.Text != original || !filepath.IsAbs(res.Workdir) {
		t.Fatal("streams, workdir, outcome, duration or diagnostics changed")
	}
	backend := &ctxInlinePersistence{}
	ctx := core.WithOutputPersistence(context.Background(), backend)
	content := core.NewOutputStore().ModelContentContext(ctx, "ctx_execute", result)
	if content != result.ModelText || core.StoredOutputHandle(content) != "" || backend.saves != 0 || backend.reads != 0 {
		t.Fatal("complete small result unnecessarily retained or read")
	}
	serialized, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(serialized), `\"command\"`) {
		t.Fatal("live UI serialization lost the original command")
	}
	t.Logf("inline result %d -> %d bytes; saved %d with no output handle", len(original), len(content), len(original)-len(content))
}

func TestCtxExecuteInlineModelTextGuards(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ctxexec.Result, *Result, *[]string)
	}{
		{"short command", func(r *ctxexec.Result, _ *Result, c *[]string) {
			*c = []string{"git", "status", "--short"}
			r.Command = strings.Join(*c, " ")
		}},
		{"changed runner command", func(r *ctxexec.Result, _ *Result, _ *[]string) { r.Command = "cmd /c " + r.Command }},
		{"different quoting", func(r *ctxexec.Result, _ *Result, _ *[]string) { r.Command = `"` + r.Command + `"` }},
		{"missing parsed command", func(_ *ctxexec.Result, _ *Result, c *[]string) { *c = nil }},
		{"nonzero exit", func(r *ctxexec.Result, _ *Result, _ *[]string) { r.ExitCode = 7 }},
		{"runner error", func(r *ctxexec.Result, _ *Result, _ *[]string) { r.Error = "unknown outcome" }},
		{"tool error", func(_ *ctxexec.Result, r *Result, _ *[]string) { r.Err = errors.New("verification failed") }},
		{"retained evidence", func(_ *ctxexec.Result, r *Result, _ *[]string) { r.RetainedText = "full capture" }},
		{"structured preview", func(_ *ctxexec.Result, r *Result, _ *[]string) { r.ModelPreview = "bounded capture" }},
		{"larger original", func(_ *ctxexec.Result, r *Result, _ *[]string) {
			r.Text = strings.Repeat("x", core.ModelOutputInlineBytes+1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command, raw, result := ctxInlineFixture()
			tc.change(raw, &result, &command)
			if got := ctxExecuteInlineModelText(raw, command, result); got != "" {
				t.Fatal("non-identical command or existing evidence contract was changed")
			}
		})
	}
	if got := ctxExecuteInlineModelText(nil, nil, Result{}); got != "" {
		t.Fatal("nil result must not produce model text")
	}
}

func TestCtxExecuteRegistryInlineModelText(t *testing.T) {
	for _, encoded := range []bool{false, true} {
		t.Run(map[bool]string{false: "native argv", true: "JSON encoded argv"}[encoded], func(t *testing.T) {
			home := t.TempDir()
			registry := NewRegistry()
			registry.MustRegister(NewCtxExecuteTool(ctxexec.New(home), home).Spec())
			command := []string{os.Args[0], "-test.run=^TestCommandPreviewHelper$", "--", strings.Repeat("fixture-argument-", 40)}
			var value any = command
			if encoded {
				body, _ := json.Marshal(command)
				value = string(body)
			}
			args, _ := json.Marshal(map[string]any{"command": value, "env_extra": []string{"SUPERCLI_COMMAND_PREVIEW_HELPER=small"}})
			result, err := registry.Execute(context.Background(), "ctx_execute", args)
			if err != nil || result.Err != nil || result.ModelText == "" {
				t.Fatalf("inline command execution/coercion failed: %v / %v", err, result.Err)
			}
			var raw, compact ctxexec.Result
			if json.Unmarshal([]byte(result.Text), &raw) != nil || json.Unmarshal([]byte(result.ModelText), &compact) != nil {
				t.Fatal("invalid JSON")
			}
			if raw.Command != strings.Join(command, " ") || compact.Command != "" || !filepath.IsAbs(compact.Workdir) {
				t.Fatal("argv or resolved workdir lost")
			}
			compact.Command = raw.Command
			if !reflect.DeepEqual(raw, compact) {
				t.Fatal("execution evidence changed")
			}
			content := registry.ModelResultContent("ctx_execute", result)
			if content != result.ModelText || core.StoredOutputHandle(content) != "" {
				t.Fatal("small successful result needs no read_output")
			}
		})
	}
}

type ctxInlineWireCapture struct{ body []byte }

func (c *ctxInlineWireCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	var err error
	c.body, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: req}, nil
}
func ctxInlineWire(t *testing.T, name, args, content string) map[string]any {
	t.Helper()
	capture := &ctxInlineWireCapture{}
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: "http://127.0.0.1:1234/v1", Model: "ctx-inline-fixture",
		HTTPClient: &http.Client{Transport: capture},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "Find the call site."},
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
	var body map[string]any
	if err := json.Unmarshal(capture.body, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCtxExecuteInlineModelTextKeepsProtocolPairs(t *testing.T) {
	command, raw, result := ctxInlineFixture()
	args, _ := json.Marshal(map[string]any{"command": command})
	wrapped, _ := json.Marshal(map[string]any{"tool": "ctx_execute", "args": json.RawMessage(args)})
	after := ctxExecuteInlineModelText(raw, command, result)
	for _, tc := range []struct{ name, args string }{{"ctx_execute", string(args)}, {"invoke_tool", string(wrapped)}} {
		t.Run(tc.name, func(t *testing.T) {
			beforeWire := ctxInlineWire(t, tc.name, tc.args, result.Text)
			afterWire := ctxInlineWire(t, tc.name, tc.args, after)
			beforeMessages := beforeWire["messages"].([]any)
			afterMessages := afterWire["messages"].([]any)
			beforeResult := beforeMessages[len(beforeMessages)-1].(map[string]any)
			afterResult := afterMessages[len(afterMessages)-1].(map[string]any)
			if afterResult["content"] != after || afterResult["tool_call_id"] != "call_fixture" || afterResult["name"] != tc.name {
				t.Fatal("result was no longer paired with its call")
			}
			// Substituting only the shorter result restores the entire original
			// request; the provider sees exactly the same command and all settings.
			afterResult["content"] = beforeResult["content"]
			if !reflect.DeepEqual(beforeWire, afterWire) {
				t.Fatal("protocol call/arguments or other request fields changed")
			}
		})
	}
}

var ctxInlineModelTextSink string

func BenchmarkCtxExecuteInlineModelText(b *testing.B) {
	for _, long := range []bool{false, true} {
		b.Run(map[bool]string{false: "short_unchanged", true: "long_duplicate"}[long], func(b *testing.B) {
			command, raw, result := ctxInlineFixture()
			if !long {
				command = []string{"git", "status", "--short"}
				raw.Command = strings.Join(command, " ")
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ctxInlineModelTextSink = ctxExecuteInlineModelText(raw, command, result)
			}
		})
	}
}
