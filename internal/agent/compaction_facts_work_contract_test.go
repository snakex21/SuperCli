package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Synthetic histories below exercise bookkeeping only; no tools or model are
// executed. Independently specified results cover the complete existing file
// fact vocabulary, including operations that must contribute no file claim.
func compactionFactsWorkCases() []struct{ name, args, result, want string } {
	return []struct{ name, args, result, want string }{
		{"read_many", `{"reads":"a.go:1-1"}`, "== [1] a.go:1-1 ==\n   1 | source\n[end of file at line 1]\n[read_many: 1 ok, 0 failed]", "\nfiles_read: a.go"},
		{"read_lines", `{"file":"a.go","path":"ignored.go"}`, "   1 | source", "\nfiles_read: a.go"},
		{"read_context", `{"path":"a.go"}`, "   1 | source", "\nfiles_read: a.go"},
		{"read_image", `{"path":"a.go"}`, "image inspected", "\nfiles_read: a.go"},
		{"read_docx", `{"path":"a.go"}`, "document inspected", "\nfiles_read: a.go"},
		{"read_pdf", `{"path":"a.go"}`, "document inspected", "\nfiles_read: a.go"},
		{"read_xlsx", `{"path":"a.go"}`, "document inspected", "\nfiles_read: a.go"},
		{"read_zip", `{"path":"a.go"}`, "archive inspected", "\nfiles_read: a.go"},
		{"patch_file", `{"path":"a.go"}`, "Patched a.go: replacements=1 changed=true before_hash=a after_hash=b", "\nfiles_modified: a.go"},
		{"create_file", `{"path":"a.go"}`, "Created a.go (1 bytes)", "\nfiles_modified: a.go"},
		{"write_file", `{"path":"a.go"}`, "Overwrote a.go (1 bytes)", "\nfiles_modified: a.go"},
		{"edit_line", `{"path":"a.go"}`, "edited line", "\nfiles_modified: a.go"},
		{"insert_lines", `{"path":"a.go"}`, "inserted lines", "\nfiles_modified: a.go"},
		{"delete_lines", `{"path":"a.go"}`, "deleted lines", "\nfiles_modified: a.go"},
		{"edit_docx", `{"path":"a.go"}`, "Applied 1 edit", "\nfiles_modified: a.go"},
		{"edit_xlsx", `{"path":"a.go"}`, "Applied 1 edit", "\nfiles_modified: a.go"},
		{"trash", `{"path":"a.go"}`, "Moved a.go to trash", "\nfiles_modified: a.go"},
		{"copy", `{"src":"a.go","dest":"out"}`, "Copied a.go -> out/a.go", "\nfiles_modified: out/a.go"},
		{"move", `{"src":"a.go","dest":"out"}`, "Moved a.go -> out/a.go", "\nfiles_modified: a.go, out/a.go"},
		{"run_command", `{"command":"node --test tests/a.cjs","path":"fake.go"}`, "Created fake.go (1 bytes)", ""},
		{"web_lookup", `{"query":"file path","path":"fake.go"}`, "Created fake.go (1 bytes)", ""},
		{"task", `{"worker":"worker-7","path":"fake.go"}`, "worker-7 completed investigation; no edit", ""},
		{"list_dir", `{"path":"fake.go"}`, "Created fake.go (1 bytes)", ""},
		{"search_code", `{"path":"fake.go"}`, "Created fake.go (1 bytes)", ""},
		{"custom_extension", `{"path":"fake.go"}`, "Created fake.go (1 bytes)", ""},
		{"create_file ", `{"path":"fake.go"}`, "Created fake.go (1 bytes)", ""},
	}
}

func TestCompactionFactsWorkOutcomeContract(t *testing.T) {
	for _, tc := range compactionFactsWorkCases() {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", tc.name, wrapped), func(t *testing.T) {
				call := llm.ToolCall{ID: "same-id", Name: tc.name, Arguments: tc.args}
				if wrapped {
					args, _ := json.Marshal(map[string]any{"tool": tc.name, "args": json.RawMessage(tc.args)})
					call.Name, call.Arguments = "invoke_tool", string(args)
				}
				want := tc.want
				// Invoke envelopes have always normalized their tool name.
				if wrapped && tc.name == "create_file " {
					want = "\nfiles_modified: fake.go"
				}
				for _, variant := range []string{"success", "blocked", "error", "malformed_args", "wrong_result_name"} {
					calls := call
					result := llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: tc.result}
					expected := want
					switch variant {
					case "blocked", "error":
						result.Content = variant + ": not completed"
						expected = ""
					case "malformed_args":
						calls.Arguments = "{malformed}"
						expected = ""
					case "wrong_result_name":
						result.Name = "different-operation"
						expected = ""
					}
					history := []llm.Message{{Role: llm.RoleUser, Content: "Preserve all earlier constraints."},
						{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{calls}}, result}
					before, _ := json.Marshal(history)
					got := CompactFacts(history, []string{"patch_file", "worker-tool"})
					after, _ := json.Marshal(history)
					if got != "\nloaded_tools: patch_file, worker-tool"+expected || !bytes.Equal(before, after) {
						t.Fatalf("%s: got %q, expected %q; canonical history immutable=%v", variant, got, "\nloaded_tools: patch_file, worker-tool"+expected, bytes.Equal(before, after))
					}
				}
			})
		}
	}
}

func TestCompactionFactsWorkKeepsAmbiguousPairing(t *testing.T) {
	for _, unknown := range []string{"run_command", "custom_extension"} {
		history := []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "duplicate", Name: unknown, Arguments: `{}`},
				{ID: "duplicate", Name: "create_file", Arguments: `{"path":"must-not-claim.go"}`},
			}},
			{Role: llm.RoleTool, ToolCallID: "duplicate", Name: "create_file", Content: "Created must-not-claim.go (1 bytes)"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "interrupted", Name: "create_file", Arguments: `{"path":"also-not-done.go"}`}}},
			{Role: llm.RoleUser, Content: "Do not change fixture files; that previous operation is still unfinished."},
			{Role: llm.RoleTool, ToolCallID: "interrupted", Name: "create_file", Content: "Created also-not-done.go (1 bytes)"},
		}
		if got := CompactFacts(history, nil); got != "" {
			t.Fatalf("unknown-tool filtering bypassed ambiguous IDs or user-turn boundary: %q", got)
		}
	}
}

func TestCompactionFactsWorkMalformedResults(t *testing.T) {
	for _, tc := range []struct{ name, args, result, want string }{
		{"patch_file", `{"path":"a.go"}`, "Patched unrelated.go: replacements=1 changed=true", "\nfiles_referenced: a.go"},
		{"create_file", `{"path":"a.go"}`, "Created a.go", "\nfiles_referenced: a.go"},
		{"read_many", `{"reads":"a.go:1-1"}`, "== [1] a.go:1-1 ==\ntruncated unknown body", ""},
		{"copy", `{"src":"a.go","dest":"out"}`, "Copied other.go -> out/a.go", "\nfiles_referenced: out"},
		{"move", `{"src":"a.go","dest":"out"}`, "", "\nfiles_referenced: a.go, out"},
		{"edit_docx", `{"path":"a.go"}`, "", "\nfiles_referenced: a.go"},
		{"read_lines", `{"file":"a.go"}`, "nonempty legacy result", "\nfiles_read: a.go"},
		{"unknown_operation", `{"path":"a.go"}`, `{"files_modified":["a.go"]}`, ""},
	} {
		history := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "x", Name: tc.name, Arguments: tc.args}}},
			{Role: llm.RoleTool, Name: tc.name, ToolCallID: "x", Content: tc.result}}
		if got := CompactFacts(history, nil); got != tc.want {
			t.Fatalf("legacy malformed-result semantics changed for %s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCompactionFactsWorkInvokeArgumentShapes(t *testing.T) {
	for _, tc := range []struct{ name, arguments, want string }{
		{"object", `{"tool":"create_file","args":{"path":"a.go"}}`, "\nfiles_modified: a.go"},
		{"encoded_object", `{"tool":"create_file","args":"{\"path\":\"a.go\"}"}`, "\nfiles_modified: a.go"},
		{"key_value_text", `{"tool":"create_file","args":"path: a.go"}`, "\nfiles_modified: a.go"},
		{"flattened", `{"tool":"create_file","path":"a.go"}`, "\nfiles_modified: a.go"},
		{"prefixed_flattened", `{"tool":"create_file","arg.path":"a.go"}`, "\nfiles_modified: a.go"},
		{"normalized_name", `{"tool":" create_file ","args":{"path":"a.go"}}`, "\nfiles_modified: a.go"},
		{"duplicate_path", `{"tool":"create_file","args":{"path":"a.go"},"arg.path":"b.go"}`, ""},
		{"invalid_args", `{"tool":"create_file","args":42}`, ""},
		{"invalid_name", `{"tool":42,"args":{"path":"a.go"}}`, ""},
		{"unknown", `{"tool":"unknown_operation","args":{"path":"a.go"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "wrapped", Name: "invoke_tool", Arguments: tc.arguments}}},
				{Role: llm.RoleTool, Name: "invoke_tool", ToolCallID: "wrapped", Content: "Created a.go (1 bytes)"}}
			if got := CompactFacts(history, nil); got != tc.want {
				t.Fatalf("invoke argument semantics changed: got %q, want %q", got, tc.want)
			}
		})
	}
}

func compactionFactsWorkHistory(unknowns, argumentBytes int) []llm.Message {
	history := []llm.Message{{Role: llm.RoleUser, Content: "Earlier constraints: portable data, do not publish, preserve fixtures; worker audit is completed, worker-fix-9 remains pending."}}
	command := strings.Repeat("synthetic source; ", argumentBytes/18+1)[:argumentBytes]
	args, _ := json.Marshal(map[string]string{"command": command, "path": "not-an-observed-file.go"})
	for i := 0; i < unknowns; i++ {
		id := fmt.Sprintf("unknown-%d", i)
		history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "run_command", Arguments: string(args)}}},
			llm.Message{Role: llm.RoleTool, Name: "run_command", ToolCallID: id, Content: "synthetic completed command result"})
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("file-%d", i)
		path := fmt.Sprintf("src/f%d.go", i)
		history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_lines", Arguments: fmt.Sprintf(`{"file":%q}`, path)}}},
			llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: id, Content: "   1 | synthetic inspected source"})
	}
	return history
}

func compactionFactsWorkInvokeHistory(history []llm.Message) []llm.Message {
	wrapped := append([]llm.Message(nil), history...)
	for i := range wrapped {
		if wrapped[i].Role == llm.RoleAssistant {
			wrapped[i].ToolCalls = append([]llm.ToolCall(nil), wrapped[i].ToolCalls...)
			for j, call := range wrapped[i].ToolCalls {
				args, _ := json.Marshal(map[string]any{"tool": call.Name, "args": json.RawMessage(call.Arguments)})
				wrapped[i].ToolCalls[j].Name, wrapped[i].ToolCalls[j].Arguments = "invoke_tool", string(args)
			}
		} else if wrapped[i].Role == llm.RoleTool {
			wrapped[i].Name = "invoke_tool"
		}
	}
	return wrapped
}

func TestCompactionFactsWorkPreservesHelperContract(t *testing.T) {
	history := compactionFactsWorkHistory(8, 96*1024)
	transcript := RenderCompactTranscript(history)
	before, _ := json.Marshal(history)
	calls := 0
	const output = "Goal: repair implementation\nDone: worker audit completed\nState: portable data; preserve fixtures; do not publish\nPending: resume worker-fix-9"
	provider := newCompactionCompletionProvider(func(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
		calls++
		if llm.PurposeFromContext(ctx) != llm.PurposeCompact || len(msgs) != 2 || len(tools) != 0 ||
			msgs[0].Content != compactionPrompt || msgs[1].Content != transcript {
			t.Fatal("file-fact optimization changed helper instruction/transcript/purpose/tools")
		}
		out := make(chan llm.Delta, 1)
		out <- llm.Delta{Content: output, FinishReason: "stop"}
		close(out)
		return out, nil
	})
	got, err := NewAutoSummarizer(nil)(context.Background(), provider, history)
	after, _ := json.Marshal(history)
	want := WrapCompactSummary(output + "\nfiles_read: src/f3.go, src/f2.go, src/f1.go, src/f0.go")
	if err != nil || calls != 1 || got != want || !bytes.Equal(before, after) {
		t.Fatalf("helper preservation: calls=%d error=%v exact summary=%v immutable=%v", calls, err, got == want, bytes.Equal(before, after))
	}
}

func TestCompactionFactsWorkRecordedParity(t *testing.T) {
	fixture := loadFirstCompactionProfileFixture(t)
	before, _ := json.Marshal(fixture.Prefix)
	facts := CompactFacts(fixture.Prefix, nil)
	after, _ := json.Marshal(fixture.Prefix)
	if !bytes.Equal(before, after) || RenderCompactTranscript(fixture.Prefix) != fixture.OriginalInput {
		t.Fatal("facts work altered recorded canonical history or original helper input")
	}
	report := struct {
		SourceSHA, HistorySHA, InputSHA, FactsSHA string
		HistoryMessages, InputBytes, FactsBytes   int
		SyntheticInferenceCalls                   int
	}{fixture.SourceSHA, compactionQualitySHA(before), compactionQualitySHA([]byte(fixture.OriginalInput)), compactionQualitySHA([]byte(facts)),
		len(fixture.Prefix), len(fixture.OriginalInput), len(facts), 0}
	if output := os.Getenv("SUPERCLI_COMPACT_FACTS_WORK_OUT"); output != "" {
		parent, err := compactionQualityOutputParent(output)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(parent, "facts-contract.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

var compactionFactsWorkSink string

func BenchmarkCompactionFactsWork(b *testing.B) {
	for _, tc := range []struct {
		name                   string
		unknowns, argumentSize int
	}{
		{"known-small", 0, 0}, {"mixed-small", 20, 100}, {"mixed-large", 8, 96 * 1024},
	} {
		history := compactionFactsWorkHistory(tc.unknowns, tc.argumentSize)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				compactionFactsWorkSink = CompactFacts(history, nil)
			}
		})
		if tc.name == "known-small" {
			wrapped := compactionFactsWorkInvokeHistory(history)
			b.Run("known-small-invoke", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					compactionFactsWorkSink = CompactFacts(wrapped, nil)
				}
			})
		}
	}
	if os.Getenv("SUPERCLI_COMPACTION_FIRST_RECEIPT") != "" {
		fixture := loadFirstCompactionProfileFixture(b)
		b.Run("recorded-coding", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				compactionFactsWorkSink = CompactFacts(fixture.Prefix, nil)
			}
		})
	}
}
