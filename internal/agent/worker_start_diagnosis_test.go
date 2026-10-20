package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// These fixtures stop at preparation: no provider request, worker goroutine,
// preflight, persistence, or completion path is involved.
var errWorkerStartDiagnosis = errors.New("diagnosis: stop at child factory")

func workerStartSeedDiagnosis(t testing.TB, count int, advisor bool) (*AgentTool, json.RawMessage, *int) {
	t.Helper()
	parent := &Loop{Messages: make([]llm.Message, count+1)}
	parent.Messages[0] = llm.Message{Role: llm.RoleSystem, Content: "parent system"}
	for i := 1; i <= count; i++ {
		role := llm.RoleUser
		if i%2 == 0 {
			role = llm.RoleAssistant
		}
		parent.Messages[i] = llm.Message{Role: role, Content: "fixed visible history"}
	}
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	seen := new(int)
	a, err := NewAgentTool(reg, parent, tools.NewRegistry(), &stubReplyProvider{name: "local-diagnosis"}, nil,
		func(cfg LoopConfig) (*Loop, error) {
			*seen = len(cfg.InitialMessages)
			return nil, errWorkerStartDiagnosis
		})
	if err != nil {
		t.Fatal(err)
	}
	a.TimeoutPerStep = 0
	args := json.RawMessage(fmt.Sprintf(`{"prompt":"Review the supplied history","share_context":true,"advise":%t}`, advisor))
	return a, args, seen
}

func TestWorkerStartDiagnosisSeedBoundary(t *testing.T) {
	for _, advisor := range []bool{false, true} {
		t.Run(fmt.Sprintf("advisor=%t", advisor), func(t *testing.T) {
			a, args, seen := workerStartSeedDiagnosis(t, 1024, advisor)
			before, err := json.Marshal(a.ParentLoop.Messages)
			if err != nil {
				t.Fatal(err)
			}
			res, err := a.execute(context.Background(), args)
			if err != nil || !errors.Is(res.Err, errWorkerStartDiagnosis) {
				t.Fatalf("did not stop at preparation boundary: %v / %v", err, res.Err)
			}
			want := 1024
			if advisor {
				want = advisorSeedMessages
			}
			if *seen != want {
				t.Fatalf("seed messages = %d, want %d", *seen, want)
			}
			after, err := json.Marshal(a.ParentLoop.Messages)
			if err != nil || string(before) != string(after) {
				t.Fatal("preparation changed parent history")
			}
		})
	}
}

func BenchmarkWorkerStartDiagnosisSeed(b *testing.B) {
	for _, advisor := range []bool{false, true} {
		for _, count := range []int{0, 1024} {
			b.Run(fmt.Sprintf("advisor=%t/messages=%d", advisor, count), func(b *testing.B) {
				a, args, _ := workerStartSeedDiagnosis(b, count, advisor)
				b.ReportAllocs()
				for b.Loop() {
					res, err := a.execute(context.Background(), args)
					if err != nil || !errors.Is(res.Err, errWorkerStartDiagnosis) {
						b.Fatalf("preparation: %v / %v", err, res.Err)
					}
				}
			})
		}
	}
}

func workerStartSourceDiagnosis(t testing.TB, reads int) (*AgentTool, llm.ToolCall, string) {
	t.Helper()
	root := t.TempDir()
	const path = "unrelated.go"
	if err := os.WriteFile(filepath.Join(root, path), []byte(strings.Repeat("// fixed unrelated source line\n", 300)), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reader := tools.NewReadLines(root).Spec()
	reg.MustRegister(reader)
	call := llm.ToolCall{Name: reader.Name, Arguments: `{"file":"unrelated.go","from":1,"to":300}`}
	result, err := reader.Fn(context.Background(), json.RawMessage(call.Arguments))
	if err != nil || result.Err != nil {
		t.Fatalf("fixture reader: %v / %v", err, result.Err)
	}
	if got := len(sourceRows(call, result.Text, root)); got != 300 {
		t.Fatalf("fixture parsed rows = %d, want 300", got)
	}
	parent := &Loop{baseDir: root, registry: reg}
	for i := 0; i < reads; i++ {
		copyCall := call
		copyCall.ID = fmt.Sprintf("read-%d", i)
		parent.Messages = append(parent.Messages,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{copyCall}},
			llm.Message{Role: llm.RoleTool, Name: reader.Name, ToolCallID: copyCall.ID, Content: result.Text})
	}
	return &AgentTool{ParentLoop: parent, BaseRegistry: reg}, call, result.Text
}

func TestWorkerStartDiagnosisUnrelatedSource(t *testing.T) {
	a, _, _ := workerStartSourceDiagnosis(t, 16)
	if got := a.workerSourceContext(context.Background(), "Review target.go", SubAgent{}); got != "" {
		t.Fatalf("unrelated evidence leaked: %s", got)
	}
	if got := a.workerSourceContext(context.Background(), "Review unrelated.go", SubAgent{}); !strings.Contains(got, "fixed unrelated source line") || len(got) > workerSourceBytes {
		t.Fatalf("relevant evidence missing or unbounded: %d bytes", len(got))
	}
}

func BenchmarkWorkerStartDiagnosisUnrelatedSource(b *testing.B) {
	for _, reads := range []int{0, 1, 16} {
		b.Run(fmt.Sprintf("reads=%d/rows=300", reads), func(b *testing.B) {
			a, _, _ := workerStartSourceDiagnosis(b, reads)
			b.ReportAllocs()
			for b.Loop() {
				if got := a.workerSourceContext(context.Background(), "Review target.go", SubAgent{}); got != "" {
					b.Fatal("unrelated source was transferred")
				}
			}
		})
	}
}

func BenchmarkWorkerStartDiagnosisSourceParseOnly(b *testing.B) {
	a, call, content := workerStartSourceDiagnosis(b, 1)
	b.ReportAllocs()
	for b.Loop() {
		if rows := sourceRows(call, content, a.ParentLoop.baseDir); len(rows) != 300 {
			b.Fatal("fixture lost rows")
		}
	}
}
