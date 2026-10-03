package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// The admission switch must follow every supported footprint case. This
// structural guard catches a newly supported tool whose arguments would
// otherwise be silently skipped by the fast path.
func TestFileFootprintAdmissionNamesStayAligned(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "loop_tool_schedule.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, declaration := range file.Decls {
		if candidate, ok := declaration.(*ast.FuncDecl); ok && candidate.Name.Name == "fileAccessesForCall" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("fileAccessesForCall not found")
	}
	var names []map[string]bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		selector, ok := statement.Tag.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Name" {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "call" {
			return true
		}
		supported := make(map[string]bool)
		for _, clause := range statement.Body.List {
			for _, expression := range clause.(*ast.CaseClause).List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatal("expected literal tool names in footprint switches")
				}
				name, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				supported[name] = true
			}
		}
		names = append(names, supported)
		return true
	})
	if len(names) != 2 {
		t.Fatalf("expected admission and footprint switches; found %d", len(names))
	}
	if !reflect.DeepEqual(names[0], names[1]) {
		t.Fatalf("admission differs from supported footprints: admitted=%v supported=%v", names[0], names[1])
	}
}

func scheduleGateFixture(t testing.TB, size int, extension bool) (*Loop, []llm.ToolCall) {
	t.Helper()
	name := "ctx_execute"
	if extension {
		name = "extension_fixture"
	}
	registry := tools.NewRegistry()
	fn := func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "fixture evidence"}, nil
	}
	verify := func(tools.Result) tools.VerifyVerdict {
		return tools.VerifyVerdict{OK: true}
	}
	registry.MustRegister(tools.Tool{
		Name: name, Description: "Controlled fixture", Schema: "{}", Fn: fn, Verify: verify,
	})
	registry.MarkAlwaysOn(name)
	registry.MustRegister(tools.Tool{
		Name: "read_lines", Description: "Controlled fixture read", Schema: "{}",
		ReadOnly: true, Fn: fn, Verify: verify,
	})
	registry.MarkAlwaysOn("read_lines")
	loop, err := NewLoop(LoopConfig{
		Provider: echoProvider("fixture"), Registry: registry, BaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"command": []string{"fixture-command", strings.Repeat("x", size)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return loop, []llm.ToolCall{
		{ID: "unknown", Name: name, Arguments: string(raw)},
		{ID: "known", Name: "read_lines", Arguments: "{\"file\":\"fixture.txt\"}"},
	}
}

func TestScheduleGateUnknownMalformedAndKnownParity(t *testing.T) {
	unknownNames := []string{
		"ctx_execute", "task", "send_message", "apply_skill", "search_code",
		"invoke_tool", "extension_fixture", "", "WRITE_FILE",
	}
	unknownArgs := []string{
		"{}", "null", "[]", "{broken", "{\"command\":\"Żółć\\u0000\"}",
		"{\"args\":{\"payload\":\"" + strings.Repeat("x", 32768) + "\"}}",
	}
	for _, name := range unknownNames {
		for _, raw := range unknownArgs {
			access, known := fileAccessesForCall(llm.ToolCall{Name: name, Arguments: raw})
			if known || access != nil {
				t.Fatalf("unsupported name became known: %q", name)
			}
		}
	}
	cases := []struct {
		name, raw string
		want      []toolFileAccess
		known     bool
	}{
		{"patch_file", "{\"path\":\"a.txt\",\"new\":\"content\"}", []toolFileAccess{{path: "a.txt", write: true}}, true},
		{"read_lines", "{\"file\":\"a.txt\"}", []toolFileAccess{{path: "a.txt"}}, true},
		{"read_image", "{\"path\":\"a.png\"}", []toolFileAccess{{path: "a.png"}}, true},
		{"copy", "{\"src\":\"a\",\"dest\":\"b\"}", []toolFileAccess{{path: "a"}, {path: "b", write: true}}, true},
		{"move", "{\"src\":\"a\",\"dest\":\"b\"}", []toolFileAccess{{path: "a", write: true}, {path: "b", write: true}}, true},
		{"read_zip", "{\"path\":\"a.zip\",\"action\":\"extract\",\"target_dir\":\"out\"}", []toolFileAccess{{path: "a.zip"}, {path: "out", write: true}}, true},
		{"read_zip", "{\"path\":\"a.zip\",\"action\":\"unknown\"}", nil, false},
		{"read_zip", "{\"path\":\"a.zip\",\"action\":null}", nil, false},
		{"write_file", "{\"path\":5}", nil, false},
		{"write_file", "{bad", nil, false},
		{"write_file", "[]", nil, false},
		{"write_file", "null", nil, false},
		{"read_lines", "{\"FILE\":\"a\"}", nil, false},
		{"read_lines", "{\"file\":\" Żółć\\u0000.txt\"}", []toolFileAccess{{path: " Żółć\x00.txt"}}, true},
	}
	for _, test := range cases {
		access, known := fileAccessesForCall(llm.ToolCall{Name: test.name, Arguments: test.raw})
		if known != test.known || !reflect.DeepEqual(access, test.want) {
			t.Fatalf("%s footprint changed for %q: %+v/%v", test.name, test.raw, access, known)
		}
	}
}

func TestScheduleGatePublicRunEvidenceOrder(t *testing.T) {
	loop, calls := scheduleGateFixture(t, 1273, false)
	original := append([]llm.ToolCall(nil), calls...)
	var names []string
	var arguments [][32]byte
	registry := tools.NewRegistry()
	for _, name := range []string{"ctx_execute", "read_lines"} {
		registry.MustRegister(tools.Tool{
			Name: name, Description: "Controlled fixture", Schema: "{}", ReadOnly: name == "read_lines",
			Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} },
			Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				names = append(names, name)
				arguments = append(arguments, sha256.Sum256(raw))
				return tools.Result{Text: "preserved " + name + " evidence"}, nil
			},
		})
		registry.MarkAlwaysOn(name)
	}
	loop.SetRegistry(registry)
	var deltas []llm.Delta
	for _, call := range calls {
		copy := call
		deltas = append(deltas, llm.Delta{ToolCall: &copy})
	}
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		deltas, {{Content: "Finished.", FinishReason: "stop"}},
	}}
	loop.provider = provider
	drainEvents(t, mustRun(t, loop, "Run the fixture command and read the fixture result."))
	if strings.Join(names, ",") != "ctx_execute,read_lines" || len(provider.reqs) != 2 {
		t.Fatalf("dispatch/order changed: %v requests=%d", names, len(provider.reqs))
	}
	for index, call := range original {
		if arguments[index] != sha256.Sum256([]byte(call.Arguments)) {
			t.Fatal("tool arguments changed")
		}
	}
	var results []llm.Message
	for _, message := range provider.reqs[1] {
		if message.Role == llm.RoleTool {
			results = append(results, message)
		}
	}
	if len(results) != 2 {
		t.Fatalf("results=%d", len(results))
	}
	for index, message := range results {
		call := original[index]
		if message.ToolCallID != call.ID || message.Name != call.Name || message.Content != "preserved "+call.Name+" evidence" {
			t.Fatalf("history evidence changed: %+v", message)
		}
	}
}

func BenchmarkScheduleGateInvokeBatch(b *testing.B) {
	for _, extension := range []bool{false, true} {
		for _, size := range []int{256, 1273, 32 << 10, 128 << 10} {
			b.Run(fmt.Sprintf("extension=%v/size=%d", extension, size), func(b *testing.B) {
				loop, calls := scheduleGateFixture(b, size, extension)
				out := make(chan Event, 32)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					loop.Messages = loop.Messages[:0]
					ok, outcomes := loop.invokeToolCalls(context.Background(), calls, out)
					if !ok || countFailures(outcomes) != 0 {
						b.Fatal("fixture tool result failed")
					}
					for len(out) > 0 {
						<-out
					}
				}
			})
		}
	}
}

func BenchmarkScheduleGateLeaf(b *testing.B) {
	for _, size := range []int{256, 1273, 32 << 10, 128 << 10} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			raw, err := json.Marshal(map[string]any{
				"command": []string{"fixture", strings.Repeat("x", size)},
			})
			if err != nil {
				b.Fatal(err)
			}
			call := llm.ToolCall{Name: "ctx_execute", Arguments: string(raw)}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, known := fileAccessesForCall(call); known {
					b.Fatal("unsupported footprint became known")
				}
			}
		})
	}
}
