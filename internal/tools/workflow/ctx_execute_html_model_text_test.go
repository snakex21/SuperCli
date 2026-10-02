package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

func ctxHTMLModelFixture() (*ctxexec.Result, []string, Result) {
	var stdout strings.Builder
	for i := 1; i <= 24; i++ {
		fmt.Fprintf(&stdout, "<Panel id=\"Panel_%02d\" limit=\"%d\">Ready & waiting > next</Panel>\n", i, 7000+i)
	}
	raw := &ctxexec.Result{
		Stdout: stdout.String(), Stderr: "notice: café & 日本語 <ready>\n",
		Command: "render-fixture --check", Workdir: "C:\\fixture\\web-project",
		DurationMS: 47, ExitCode: 0,
	}
	encoded, _ := json.Marshal(raw)
	return raw, []string{"render-fixture", "--check"}, Result{Text: string(encoded)}
}

func TestCtxExecuteHTMLModelTextPreservesCompleteResult(t *testing.T) {
	raw, args, result := ctxHTMLModelFixture()
	original := result.Text
	result.ModelText = ctxExecuteInlineModelText(raw, args, result)
	if result.ModelText == "" {
		t.Fatal("complete HTML result was not compacted")
	}
	if len(original)-len(result.ModelText) < 512 {
		t.Fatal("insufficient compact gain")
	}
	var before, after any
	if json.Unmarshal([]byte(original), &before) != nil || json.Unmarshal([]byte(result.ModelText), &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("JSON evidence changed: %s", result.ModelText)
	}
	if result.Text != original || raw.Command != "render-fixture --check" {
		t.Fatal("original evidence changed")
	}
	model := core.NewOutputStore().ModelContent("ctx_execute", result)
	if model != result.ModelText || core.StoredOutputHandle(model) != "" {
		t.Fatal("complete result created an output handle")
	}
	body, err := json.Marshal(result)
	if err != nil || strings.Contains(string(body), "ModelText") {
		t.Fatal("model-only representation leaked to UI serialization")
	}
}

func TestCtxExecuteHTMLModelTextKeepsMismatchedCommand(t *testing.T) {
	raw, _, result := ctxHTMLModelFixture()
	view := ctxExecuteInlineModelText(raw, []string{"different", "command"}, result)
	var got ctxexec.Result
	if view == "" || json.Unmarshal([]byte(view), &got) != nil || !reflect.DeepEqual(*raw, got) {
		t.Fatal("non-duplicate command or metadata lost")
	}
}

func TestCtxExecuteHTMLModelTextPreservesCaptureWarnings(t *testing.T) {
	raw, args, result := ctxHTMLModelFixture()
	raw.TruncatedStdout = true
	raw.TruncatedStderr = true
	body, _ := json.Marshal(raw)
	result.Text = string(body)
	view := ctxExecuteInlineModelText(raw, args, result)
	var got ctxexec.Result
	if view == "" || json.Unmarshal([]byte(view), &got) != nil || !reflect.DeepEqual(*raw, got) {
		t.Fatal("capture diagnostics lost")
	}
}

func TestCtxExecuteHTMLModelTextGuardedFallback(t *testing.T) {
	cases := []struct {
		name   string
		change func(*ctxexec.Result, *Result)
	}{
		{"failure", func(raw *ctxexec.Result, result *Result) { raw.ExitCode = 1 }},
		{"capture error", func(raw *ctxexec.Result, result *Result) { raw.Error = "output failed" }},
		{"retained", func(raw *ctxexec.Result, result *Result) { result.RetainedText = "full output" }},
		{"preview", func(raw *ctxexec.Result, result *Result) { result.ModelPreview = "preview" }},
		{"large", func(raw *ctxexec.Result, result *Result) {
			result.Text = strings.Repeat("x", core.ModelOutputInlineBytes+1)
		}},
		{"plain", func(raw *ctxexec.Result, result *Result) {
			raw.Stdout = strings.Repeat("plain output\n", 100)
			raw.Stderr = ""
			body, _ := json.Marshal(raw)
			result.Text = string(body)
		}},
		{"tiny gain", func(raw *ctxexec.Result, result *Result) {
			raw.Stdout = "<one>"
			raw.Stderr = ""
			body, _ := json.Marshal(raw)
			result.Text = string(body)
		}},
		{"literal escapes", func(raw *ctxexec.Result, result *Result) {
			raw.Stdout = strings.Repeat("\\u003c", 110)
			raw.Stderr = ""
			body, _ := json.Marshal(raw)
			result.Text = string(body)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw, args, result := ctxHTMLModelFixture()
			test.change(raw, &result)
			if got := ctxExecuteInlineModelText(raw, args, result); got != "" {
				t.Fatalf("guard changed: %s", got)
			}
		})
	}
}

var ctxHTMLModelSink string

func BenchmarkCtxExecuteHTMLModelText(b *testing.B) {
	for _, kind := range []string{"plain", "html"} {
		raw, args, result := ctxHTMLModelFixture()
		if kind == "plain" {
			raw.Stdout = "checks passed\n"
			raw.Stderr = ""
			data, _ := json.Marshal(raw)
			result.Text = string(data)
		}
		b.Run(kind, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ctxHTMLModelSink = ctxExecuteInlineModelText(raw, args, result)
			}
		})
	}
}
