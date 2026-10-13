package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Exercise actual file tools, tool dispatch and provider preparation. Changing
// query/range spelling must not hide repetition, or prevent a verified edit.
func TestSourceLoopNativeAndSentinelWarnThenFinishWithFreshFile(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%t", thin), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "a.go")
			original := strings.Repeat("const Value = \""+strings.Repeat("x", 60)+"\"\n", 20)
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewReadLines(root).Spec())
			reg.MustRegister(tools.Tool{Name: "patch_file", Description: "change source fixture", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				err := os.WriteFile(path, []byte(strings.ReplaceAll(original, "const Value", "var ChangedValue")), 0600)
				return tools.Result{Text: "changed file", Err: err}, nil
			}})
			reg.MarkAlwaysOn("read_lines")
			reg.MarkAlwaysOn("patch_file")
			var scripts [][]llm.Delta
			add := func(tool, args, sentinel string) {
				if thin {
					scripts = append(scripts, []llm.Delta{{Content: sentinel, FinishReason: "stop"}})
					return
				}
				call := llm.ToolCall{ID: fmt.Sprintf("call%d", len(scripts)), Name: tool, Arguments: args}
				scripts = append(scripts, []llm.Delta{{ToolCall: &call}, {FinishReason: "tool_calls"}})
			}
			for i := 0; i < 4; i++ {
				file := []string{"a.go", "./a.go", "a.go", "./a.go"}[i]
				add("read_lines", fmt.Sprintf(`{"file":%q,"from":1,"to":%d}`, file, 20-i), fmt.Sprintf("«read_lines\nfile: %s\nfrom: 1\nto: %d»", file, 20-i))
			}
			add("patch_file", `{}`, "«patch_file»")
			add("read_lines", `{"file":"a.go","from":1,"to":20}`, "«read_lines\nfile: a.go\nfrom: 1\nto: 20»")
			scripts = append(scripts, []llm.Delta{{Content: "Changed and checked.", FinishReason: "stop"}})
			provider := &stubProvider{name: "source-loop", scripts: scripts}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, ThinTools: thin, MaxSteps: 16})
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, mustRun(t, loop, "Implement the requested change to a.go and check it."))
			warned := false
			for _, event := range events {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
				if notice, ok := event.(NoticeEvent); ok && strings.Contains(notice.Text, "loop warning") {
					warned = true
				}
			}
			if !warned || provider.calls != 7 {
				t.Fatalf("warning=%t provider calls=%d", warned, provider.calls)
			}
			if _, ok := events[len(events)-1].(DoneEvent); !ok {
				t.Fatal("run did not finish")
			}
			lastRead, repeated := "", false
			for _, message := range provider.reqs[6] {
				if message.Role == llm.RoleTool && message.Name == "read_lines" {
					lastRead = message.Content
					repeated = repeated || strings.Contains(message.Content, "already shown in tool result")
				}
			}
			if repeated || !strings.Contains(lastRead, "ChangedValue") {
				t.Fatal("wire suppressed an explicit read or lost fresh changed code")
			}
			for _, message := range loop.Messages {
				if message.Role == llm.RoleTool && strings.Contains(message.Content, "already shown in tool result") {
					t.Fatal("canonical transcript was shortened")
				}
			}
		})
	}
}

func TestSourceProgressPartialEvidenceAndHistoryRemovalRemainSafe(t *testing.T) {
	a, read := sourceFixture("a.go", "read", 1, 20)
	for _, suffix := range []string{"\nerror: missing.go", "\n[remaining results omitted]", "\n... another changed excerpt"} {
		var progress repeatProgress
		for i := 0; i < 20; i++ {
			call := a.ToolCalls[0]
			call.Arguments = fmt.Sprintf(`{"file":"a.go","from":1,"to":%d}`, 20+i)
			outcome := sourceOutcome(call, read.Content+suffix+fmt.Sprint(i))
			if signal := progress.observe([]llm.ToolCall{call}, []callOutcome{outcome}); signal == repeatAbort {
				t.Fatal("partial evidence caused a semantic stop")
			}
		}
	}
	for _, change := range []string{"hide", "compact", "prune"} {
		loop := makeLoopWithMessages(a, read)
		var progress repeatProgress
		progress.syncSourceEpoch(loop.sourceEvidenceEpoch)
		for i := 0; i < 3; i++ {
			progress.source.observe(a.ToolCalls, []callOutcome{sourceOutcome(a.ToolCalls[0], read.Content)})
		}
		switch change {
		case "hide":
			if err := loop.HideRange(0, 2); err != nil {
				t.Fatal(err)
			}
		case "compact":
			loop.CompactWithSummary("a.go was inspected")
		case "prune":
			loop.Messages[1].Content = pruneMarkerPrefix
			loop.invalidateVisibleEstimate()
		}
		progress.syncSourceEpoch(loop.sourceEvidenceEpoch)
		if len(progress.source.seen) != 0 {
			t.Fatalf("%s retained forgotten source loop evidence", change)
		}
	}
}

// A request that previously used references must be rebuilt after the original
// evidence leaves its visible context. Keep the next complete read as the new
// reference target, rather than chaining through an absent tool result.
func TestSourceRequestRestoresLinesAfterOriginalEvidenceLeavesContext(t *testing.T) {
	for _, removal := range []string{"prune", "hide", "compact", "completed omission"} {
		t.Run(removal, func(t *testing.T) {
			firstCall, first := sourceFixture("a.go", "original", 1, 50)
			secondCall, second := sourceBatchFixture("a.go", "second", 1, 50)
			thirdCall, third := sourceBatchFixture("a.go", "third", 1, 50)
			l := pruneLoop(t, 100000, 1)
			l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "inspect a.go"}, firstCall, first, secondCall, second}
			before := l.providerMessages()
			if !strings.Contains(before[4].Content, "already shown in tool result original") {
				t.Fatal("fixture did not initially reference original evidence")
			}
			switch removal {
			case "prune":
				l.Messages[2].Content = pruneMarker(first)
				l.invalidateVisibleEstimate()
			case "hide":
				if err := l.HideRange(1, 3); err != nil {
					t.Fatal(err)
				}
			case "compact":
				l.CompactWithSummary("a.go was inspected; source text is not retained")
				l.Messages = append(l.Messages, secondCall, second)
			case "completed omission":
				history := []llm.Message{{Role: llm.RoleUser, Content: "previous task"}, firstCall, first,
					{Role: llm.RoleAssistant, Content: "completed"}, {Role: llm.RoleUser, Content: "inspect again"}, secondCall, second}
				l, _, _ = resolvedCompactFixture(t, history, "", 100000)
			}
			l.Messages = append(l.Messages, thirdCall, third)
			view := l.providerMessages()
			foundSecond, foundThird := false, false
			for _, message := range view {
				if message.Role != llm.RoleTool {
					continue
				}
				switch message.ToolCallID {
				case "second":
					foundSecond = true
					if message.Content != second.Content {
						t.Fatal("next read referenced evidence that left the request")
					}
				case "third":
					foundThird = true
					if !strings.Contains(message.Content, "already shown in tool result second") || strings.Contains(message.Content, "tool result original") {
						t.Fatal("reference did not move to visible full evidence")
					}
				}
			}
			if !foundSecond || !foundThird {
				t.Fatal("active read results disappeared from the request")
			}
		})
	}
}
