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
	"supercli/internal/tools/core"
)

func TestCompactFactsUseExecutedFileOutcomes(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprint(wrapped), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"a.go", "unchanged.go"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("package fixture\nconst Value = 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reg := tools.NewRegistry()
			for _, spec := range []tools.Tool{tools.NewReadLines(root).Spec(), tools.NewReadMany(root).Spec(), tools.NewPatchFile(root).Spec(), tools.NewCreateFile(root).Spec()} {
				reg.MustRegister(spec)
			}
			var history []llm.Message
			add := func(name string, args map[string]any) {
				t.Helper()
				raw, _ := json.Marshal(args)
				call := llm.ToolCall{ID: fmt.Sprint(len(history)), Name: name, Arguments: string(raw)}
				if wrapped {
					envelope, _ := json.Marshal(map[string]any{"tool": name, "args": args})
					call.Name, call.Arguments = "invoke_tool", string(envelope)
				}
				result, err := reg.Execute(context.Background(), name, raw)
				if err != nil {
					result.Err = err
				}
				history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
					llm.Message{Role: llm.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: core.NewOutputStore().ModelContent(name, result)})
			}
			add("read_lines", map[string]any{"file": "a.go"})
			add("patch_file", map[string]any{"path": "a.go", "old": "Value = 1", "new": "Value = 2"})
			add("create_file", map[string]any{"path": "b.go", "content": "package fixture\n"})
			add("patch_file", map[string]any{"path": "unchanged.go", "old": "ABSENT", "new": "Value = 3"})
			add("read_lines", map[string]any{"file": "absent.go"})
			add("read_many", map[string]any{"reads": "a.go:1-10 | b.go:1-10 | absent_batch.go:1-10"})
			add("create_file", map[string]any{"path": "read_only.go", "content": "package fixture\n"})
			// A file supplied by the user was read, but never modified by the agent.
			if err := os.WriteFile(filepath.Join(root, "inspected.go"), []byte("package fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			add("read_lines", map[string]any{"file": "inspected.go"})

			facts := CompactFacts(history, []string{"patch_file"})
			lines := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(facts), "\n") {
				key, value, _ := strings.Cut(line, ": ")
				lines[key] = value
			}
			for _, name := range []string{"a.go", "b.go", "read_only.go"} {
				if !strings.Contains(lines["files_modified"], name) {
					t.Fatalf("lost successful modification %s: %s", name, facts)
				}
				if strings.Contains(lines["files_read"], name) {
					t.Fatalf("modified file misclassified as read: %s", facts)
				}
			}
			if !strings.Contains(lines["files_read"], "inspected.go") {
				t.Fatalf("real read_lines file argument ignored: %s", facts)
			}
			for _, name := range []string{"unchanged.go", "absent.go", "absent_batch.go"} {
				if strings.Contains(lines["files_modified"], name) || strings.Contains(lines["files_read"], name) {
					t.Fatalf("failed operation presented as accomplished: %s", facts)
				}
			}
			// The standard summarizer appends these exact facts without another call.
			provider := &outputReplayProvider{stubProvider: &stubProvider{name: "facts", scripts: [][]llm.Delta{{{Content: "Goal: inspect files\nDone: changed a.go and created b.go\nState: see exact facts\nPending: none", FinishReason: "stop"}}}}}
			summary, err := NewAutoSummarizer(func() []string { return []string{"patch_file"} })(context.Background(), provider, history)
			if err != nil || len(provider.reqs) != 1 || !strings.Contains(summary, facts) {
				t.Fatalf("summarizer facts/call count: %v calls=%d %s", err, len(provider.reqs), summary)
			}
			t.Log(facts)
		})
	}
}

func TestCompactFactsDoNotInventCompletedCalls(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "pending", Name: "create_file", Arguments: "{\"path\":\"pending.go\"}"},
			{ID: "duplicate", Name: "patch_file", Arguments: "{\"path\":\"one.go\"}"},
			{ID: "duplicate", Name: "patch_file", Arguments: "{\"path\":\"two.go\"}"},
		}},
		{Role: llm.RoleTool, Name: "patch_file", ToolCallID: "duplicate", Content: "Patched one.go"},
		{Role: llm.RoleUser, Content: "Another turn"},
		{Role: llm.RoleTool, Name: "create_file", ToolCallID: "pending", Content: "Created pending.go"},
	}
	if got := CompactFacts(history, nil); got != "" {
		t.Fatalf("unpaired/ambiguous calls treated as facts: %s", got)
	}
}

func TestCompactFactsResultBoundaries(t *testing.T) {
	cases := []struct {
		name, tool, args, result, want string
	}{
		{"failed patch", "patch_file", "{\"path\":\"bad.go\"}", "error: patch_file: no match", ""},
		{"blocked write", "create_file", "{\"path\":\"bad.go\"}", "blocked: approval declined", ""},
		{"pruned patch", "patch_file", "{\"path\":\"unknown.go\"}", "[tool result pruned: patch_file; details omitted]", "files_referenced: unknown.go"},
		{"noop", "patch_file", "{\"path\":\"same.go\"}", "Patched same.go: replacements=1 changed=false before_hash=a after_hash=a\nchanged=true ", "files_referenced: same.go"},
		{"filename is not status", "patch_file", "{\"path\":\"changed=true .go\"}", "Patched changed=true .go: replacements=1 changed=false before_hash=a after_hash=a", "files_referenced: changed=true .go"},
		{"dry run", "edit_docx", "{\"path\":\"draft.docx\",\"dry_run\":true}", "Preview only: would replace text. Nothing was written.", "files_referenced: draft.docx"},
		{"word noop", "edit_docx", "{\"path\":\"draft.docx\"}", "p1 already contains the requested text. Nothing was changed.", "files_referenced: draft.docx"},
		{"directory", "list_dir", "{\"path\":\"src\"}", "one.go\ntwo.go", ""},
		{"search root", "search_code", "{\"path\":\"src\"}", "src/main.go:1:package main", ""},
		{"unknown extension", "edit_network", "{\"path\":\"src\"}", "done", ""},
		{"wrong result name", "create_file", "{\"path\":\"bad.go\"}", "Created bad.go (4 bytes)", ""},
		{"flat envelope", "invoke_tool", "{\"tool\":\"read_lines\",\"arg.file\":\"x.go\"}", "   1 | package fixture", "files_read: x.go"},
		{"string envelope", "invoke_tool", "{\"tool\":\"read_lines\",\"args\":\"file: x.go\"}", "   1 | package fixture", "files_read: x.go"},
		{"duplicate envelope", "invoke_tool", "{\"tool\":\"read_lines\",\"args\":{\"file\":\"x.go\"},\"arg.file\":\"y.go\"}", "   1 | package fixture", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resultName := tc.tool
			if tc.name == "wrong result name" {
				resultName = "read_lines"
			}
			msgs := []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: tc.tool, Arguments: tc.args}}},
				{Role: llm.RoleTool, Name: resultName, ToolCallID: "c", Content: tc.result},
			}
			if got := strings.TrimSpace(CompactFacts(msgs, nil)); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestCompactFactsBoundedFreshPaths(t *testing.T) {
	var history []llm.Message
	add := func(path string) {
		args, _ := json.Marshal(map[string]any{"file": path})
		history = append(history,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "reused", Name: "read_lines", Arguments: string(args)}}},
			llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "reused", Content: "   1 | text"})
	}
	for i := 0; i < 25; i++ {
		add(fmt.Sprintf("src/f%02d.go", i))
	}
	add("./src/f00.go") // same normalized file now recently used
	got := CompactFacts(history, []string{"patch_file"})
	if !strings.Contains(got, "files_read: ./src/f00.go, src/f24.go") || strings.Contains(got, "src/f01.go") || strings.Count(got, "f00.go") != 1 {
		t.Fatalf("recency/dedup lost: %s", got)
	}
	add(strings.Repeat("界", compactFactsMaxBytes))
	add("a,\nfiles_modified: fake.go")
	got = CompactFacts(history, []string{"patch_file"})
	if len(got) > compactFactsMaxBytes || !strings.Contains(got, "loaded_tools: patch_file") || strings.Contains(got, "\nfiles_modified:") || !strings.Contains(got, "src/f24.go") {
		t.Fatalf("budget/escaping violated (%d bytes): %s", len(got), got)
	}
}

func TestCompactFactsReadManyPreview(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.go", "two.go", "three.go"} {
		body := strings.Repeat("a long enough line to force a balanced preview\n", 300)
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tool := tools.NewReadMany(root).Spec()
	args := json.RawMessage("{\"reads\":\"one.go:1-300 | missing.go:1-20 | two.go:1-300 | three.go:1-300\"}")
	result, err := tool.Fn(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("partial batch failed: %v %v", err, result.Err)
	}
	store := core.NewOutputStore()
	content := store.ModelContent("read_many", result)
	if core.StoredOutputHandle(content) == "" {
		t.Fatal("fixture did not exercise model preview")
	}
	history := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: "read_many", Arguments: string(args)}}},
		{Role: llm.RoleTool, Name: "read_many", ToolCallID: "c", Content: content},
	}
	got := CompactFacts(history, nil)
	for _, name := range []string{"one.go", "two.go", "three.go"} {
		if !strings.Contains(got, name) {
			t.Fatalf("lost successful sibling %s: %s", name, got)
		}
	}
	if strings.Contains(got, "missing.go") {
		t.Fatalf("failed sibling reported as read: %s", got)
	}
	// After pruning only aggregate counts survive; do not guess which path failed.
	history[1].Content = pruneMarker(history[1])
	if got := CompactFacts(history, nil); got != "" {
		t.Fatalf("pruned mixed batch invented per-file outcomes: %s", got)
	}
}
