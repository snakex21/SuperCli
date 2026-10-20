package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func fileListFixture(root, id, query, content string) (llm.Message, llm.Message) {
	args, _ := json.Marshal(map[string]any{"path": root, "query": query, "output_mode": "files"})
	call := llm.ToolCall{ID: id, Name: "search_code", Arguments: string(args)}
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, llm.Message{Role: llm.RoleTool, Name: call.Name, ToolCallID: id, Content: content}
}

func fileListText(count int) string {
	var out strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&out, "src/package_%03d/component_%03d_test.go\n", i, i)
	}
	return out.String()
}

func TestSourceFileListProjectionKeepsFullEvidenceAndStablePrefix(t *testing.T) {
	body := fileListText(80)
	a, first := fileListFixture(".", "first-list", "alpha", body)
	b, second := fileListFixture(".", "second-list", "beta", body)
	history := []llm.Message{a, first, b, second}
	before, _ := json.Marshal(history)
	view := sourceEvidenceProjection(history, "")
	if view[1].Content != body || !strings.Contains(view[3].Content, "full output already shown in tool result first-list") {
		t.Fatalf("missing full evidence or exact-result reference: %q", view[3].Content)
	}
	if len(view) != len(history) || !reflect.DeepEqual(view[0], a) || !reflect.DeepEqual(view[2], b) || view[3].ToolCallID != second.ToolCallID || view[3].Name != second.Name {
		t.Fatal("projection changed tool-call protocol")
	}
	after, _ := json.Marshal(history)
	if string(after) != string(before) {
		t.Fatal("canonical history changed")
	}
	c, third := fileListFixture(".", "third-list", "gamma", body)
	next := sourceEvidenceProjection(append(history, c, third), "")
	if !reflect.DeepEqual(view, next[:len(view)]) {
		t.Fatal("appending a result changed the existing request prefix")
	}
	if !strings.Contains(next[5].Content, "first-list") || strings.Contains(next[5].Content, "second-list") {
		t.Fatal("reference points at another reference instead of full evidence")
	}

	// Byte accounting is synthetic, not measured latency or token/cost savings.
	var repeated []llm.Message
	for i := 0; i < 12; i++ {
		call, result := fileListFixture(".", fmt.Sprintf("list-%d", i), fmt.Sprintf("query-%d", i), body)
		repeated = append(repeated, call, result)
	}
	projected := sourceEvidenceProjection(repeated, "")
	rawBytes, projectedBytes := 0, 0
	for i := range repeated {
		rawBytes += len(repeated[i].Content)
		projectedBytes += len(projected[i].Content)
	}
	if projectedBytes >= rawBytes/4 {
		t.Fatalf("repeated fixture not reduced: %d -> %d", rawBytes, projectedBytes)
	}
	t.Logf("synthetic repeated file-list result text: %d -> %d UTF-8 bytes; calls and canonical results unchanged", rawBytes, projectedBytes)
	rawJSON, err := json.Marshal(repeated)
	if err != nil {
		t.Fatal(err)
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectedJSON) >= len(rawJSON) || llm.EstimateTokens(projected) >= llm.EstimateTokens(repeated) {
		t.Fatal("projected request history was not smaller")
	}
	t.Logf("synthetic serialized request history: %d -> %d bytes; estimated history tokens: %d -> %d (not provider billing or HTTP bytes)", len(rawJSON), len(projectedJSON), llm.EstimateTokens(repeated), llm.EstimateTokens(projected))
}

func TestSourceFileListProjectionPreservesChangesErrorsAndExplicitReads(t *testing.T) {
	body := fileListText(40)
	a, first := fileListFixture(".", "first-list", "alpha", body)
	cases := map[string]func(*llm.Message, *llm.Message){
		"changed": func(_ *llm.Message, r *llm.Message) { r.Content += "src/new_evidence.go\n" },
		"reordered": func(_ *llm.Message, r *llm.Message) {
			r.Content = strings.Replace(body, "component_000", "component_999", 1)
		},
		"other root": func(c *llm.Message, _ *llm.Message) {
			c.ToolCalls[0].Arguments = `{"path":"other","query":"beta","output_mode":"files"}`
		},
		"lines mode": func(c *llm.Message, _ *llm.Message) {
			c.ToolCalls[0].Arguments = `{"query":"beta","output_mode":"lines"}`
		},
		"no query": func(c *llm.Message, _ *llm.Message) {
			c.ToolCalls[0].Arguments = `{"include":"*.go","output_mode":"files"}`
		},
		"bad args":      func(c *llm.Message, _ *llm.Message) { c.ToolCalls[0].Arguments = `{` },
		"limit":         func(_ *llm.Message, r *llm.Message) { r.Content += "[search limit reached: 40 matches]\n" },
		"partial":       func(_ *llm.Message, r *llm.Message) { r.Content += "... remaining paths omitted\n" },
		"error":         func(_ *llm.Message, r *llm.Message) { r.Content += "error: src/missing.go\n" },
		"error prefix":  func(_ *llm.Message, r *llm.Message) { r.Content = "error: access denied\n" + body },
		"small":         func(_ *llm.Message, r *llm.Message) { r.Content = "src/one.go\n" },
		"pruned":        func(_ *llm.Message, r *llm.Message) { r.Content = pruneMarkerPrefix + body },
		"parts":         func(_ *llm.Message, r *llm.Message) { r.Parts = []llm.ContentPart{{Type: "text", Text: body}} },
		"explicit read": func(c *llm.Message, r *llm.Message) { c.ToolCalls[0].Name = "read_lines"; r.Name = "read_lines" },
		"recovery":      func(c *llm.Message, r *llm.Message) { c.ToolCalls[0].Name = "read_output"; r.Name = "read_output" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b, result := fileListFixture(".", "second-list", "beta", body)
			mutate(&b, &result)
			view := sourceEvidenceProjection([]llm.Message{a, first, b, result}, "")
			if !reflect.DeepEqual(view[3], result) {
				t.Fatalf("non-equivalent or unproven output changed: %q", view[3].Content)
			}
		})
	}
}

func TestSourceFileListProjectionRebuildsAfterHistoryRemovalAndResume(t *testing.T) {
	body := fileListText(40)
	a, first := fileListFixture(".", "first-list", "alpha", body)
	b, second := fileListFixture(".", "second-list", "beta", body)
	history := []llm.Message{a, first, b, second}
	view := sourceEvidenceProjection(history, "")
	for _, earlier := range []llm.Message{
		{Role: llm.RoleSystem, Content: "Summary: the file list was inspected."},
		{Role: llm.RoleTool, Name: "search_code", ToolCallID: "first-list", Content: pruneMarkerPrefix + ": omitted]"},
		view[3],
	} {
		trimmed := sourceEvidenceProjection([]llm.Message{earlier, b, second}, "")
		if trimmed[2].Content != body {
			t.Fatal("reference to removed/compacted evidence was created")
		}
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var restored []llm.Message
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if got := sourceEvidenceProjection(restored, ""); !reflect.DeepEqual(got, view) {
		t.Fatal("restored canonical history produced a different request projection")
	}
}

func TestSourceFileListActualResumeDoesNotKeepRemovedEvidence(t *testing.T) {
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := t.TempDir()
	original, err := store.Create(root, "fixture", "original")
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Create(root, "fixture", "resumed")
	if err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "file-list-resume"}, Registry: tools.NewRegistry(), BaseDir: root, Writer: session.NewWriter(store, original.ID)})
	if err != nil {
		t.Fatal(err)
	}
	body := fileListText(40)
	a, first := fileListFixture(".", "first-list", "alpha", body)
	b, second := fileListFixture(".", "second-list", "beta", body)
	history := []llm.Message{a, first, b, second}
	if err := loop.ResumeConversation(context.Background(), session.NewWriter(store, resumed.ID), history, nil); err != nil {
		t.Fatal(err)
	}
	if loop.SessionID() != resumed.ID {
		t.Fatal("resume kept the old writer")
	}
	view := sourceEvidenceProjection(loop.Messages, root)
	if !strings.Contains(view[len(view)-1].Content, "first-list") {
		t.Fatal("resumed full history lost its exact-list reference")
	}
	if err := loop.ResumeConversation(context.Background(), session.NewWriter(store, original.ID), []llm.Message{b, second}, nil); err != nil {
		t.Fatal(err)
	}
	view = sourceEvidenceProjection(loop.Messages, root)
	if view[len(view)-1].Content != body {
		t.Fatal("resuming another conversation referenced an unavailable original list")
	}
}

func TestSourceFileListProjectionDispatcherAndBound(t *testing.T) {
	body := fileListText(40)
	a, first := fileListFixture(".", "first-list", "alpha", body)
	b, second := fileListFixture(".", "second-list", "beta", body)
	b.ToolCalls[0].Name = "invoke_tool"
	b.ToolCalls[0].Arguments = `{"tool":"search_code","args":{"query":"beta","output_mode":"files"}}`
	second.Name = "invoke_tool"
	if got := sourceEvidenceProjection([]llm.Message{a, first, b, second}, ""); !strings.Contains(got[3].Content, "first-list") {
		t.Fatal("dispatcher lost file-list projection")
	}
	var history []llm.Message
	for i := 0; i <= sourceFileListingEntries; i++ {
		call, result := fileListFixture(".", fmt.Sprintf("unique-%d", i), "query", body+fmt.Sprintf("src/unique_%d.go\n", i))
		history = append(history, call, result)
	}
	call, result := fileListFixture(".", "after-bound", "again", body+"src/unique_0.go\n")
	history = append(history, call, result)
	if got := sourceEvidenceProjection(history, ""); got[len(got)-1].Content != result.Content {
		t.Fatal("evicted evidence was referenced")
	}
}

func TestSourceFileListLoopNativeAndThinKeepSearchesAndCanonicalResults(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%t", thin), func(t *testing.T) {
			body := fileListText(40)
			executions := 0
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "search_code", ReadOnly: true, Description: "fixture search", Schema: `{"type":"object","properties":{"query":{"type":"string"},"output_mode":{"type":"string"}}}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				executions++
				return tools.Result{Text: body}, nil
			}})
			reg.MarkAlwaysOn("search_code")
			var scripts [][]llm.Delta
			for i, query := range []string{"alpha", "beta"} {
				if thin {
					scripts = append(scripts, []llm.Delta{{Content: fmt.Sprintf("«search_code\nquery: %s\noutput_mode: files»", query), FinishReason: "stop"}})
				} else {
					call := llm.ToolCall{ID: fmt.Sprintf("list-%d", i), Name: "search_code", Arguments: fmt.Sprintf(`{"query":%q,"output_mode":"files"}`, query)}
					scripts = append(scripts, []llm.Delta{{ToolCall: &call}, {FinishReason: "tool_calls"}})
				}
			}
			scripts = append(scripts, []llm.Delta{{Content: "Both searches matched the same files.", FinishReason: "stop"}})
			provider := &stubProvider{name: "file-list-loop", scripts: scripts}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: t.TempDir(), ThinTools: thin, MaxSteps: 8})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Find the owners using both queries.")) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if executions != 2 || provider.calls != 3 {
				t.Fatalf("execution changed: tools=%d provider=%d", executions, provider.calls)
			}
			full, reference := false, false
			for _, message := range provider.reqs[2] {
				full = full || strings.Contains(message.Content, body)
				reference = reference || strings.Contains(message.Content, "Identical complete file list")
			}
			if !full || !reference {
				t.Fatal("actual provider request lost full evidence or was not reduced")
			}
			for _, message := range loop.Messages {
				if message.Role == llm.RoleTool && message.Content != body {
					t.Fatal("canonical tool result was changed")
				}
			}
		})
	}
}
