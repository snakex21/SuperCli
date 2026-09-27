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

func pathScheduleCall(id, name string, args map[string]any) llm.ToolCall {
	raw, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}
}

func pathScheduleFixture(t testing.TB) (*Loop, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	for _, tool := range []tools.Tool{tools.NewWriteFile(root).Spec(), tools.NewReadLines(root).Spec(), tools.NewMakeDir(root).Spec(), tools.NewCopy(root).Spec(), tools.NewMove(root).Spec()} {
		reg.MustRegister(tool)
		reg.MarkAlwaysOn(tool.Name)
		reg.Activate(tool.Name)
	}
	l, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, BaseDir: root})
	if err != nil {
		t.Fatal(err)
	}
	return l, root
}

// Each pair targets the same real filesystem resource, despite different argument
// spelling. Running both calls in one wave permits stale reads or reordered edits.
func TestFileBatchAliasesStayOrdered(t *testing.T) {
	l, root := pathScheduleFixture(t)
	write := func(path string) llm.ToolCall {
		return pathScheduleCall("first", "write_file", map[string]any{"path": path, "content": "after\n"})
	}
	read := func(path string) llm.ToolCall {
		return pathScheduleCall("second", "read_lines", map[string]any{"file": path})
	}
	absolute := filepath.Join(root, "a.txt")
	cases := []struct {
		name  string
		calls []llm.ToolCall
	}{
		{"relative then absolute read", []llm.ToolCall{write("a.txt"), read(absolute)}},
		{"absolute then relative read", []llm.ToolCall{write(absolute), read("a.txt")}},
		{"relative then absolute write", []llm.ToolCall{write("a.txt"), write(absolute)}},
		{"read before write", []llm.ToolCall{read(absolute), write("a.txt")}},
		{"new file", []llm.ToolCall{write("new/nested.txt"), read(filepath.Join(root, "new", "nested.txt"))}},
		{"project root", []llm.ToolCall{pathScheduleCall("first", "make_dir", map[string]any{"path": "."}), read("a.txt")}},
		{"normalized relative", []llm.ToolCall{write("./folder/../a.txt"), read("a.txt")}},
		{"copy destination", []llm.ToolCall{pathScheduleCall("first", "copy", map[string]any{"src": "a.txt", "dest": "b.txt"}), read(filepath.Join(root, "b.txt"))}},
		{"move source", []llm.ToolCall{pathScheduleCall("first", "move", map[string]any{"src": "a.txt", "dest": "b.txt"}), read(absolute)}},
	}
	hardLinkErr := os.Link(absolute, filepath.Join(root, "hard.txt"))
	cases = append(cases, struct {
		name  string
		calls []llm.ToolCall
	}{"hard link", []llm.ToolCall{write("a.txt"), read("hard.txt")}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "hard link" && hardLinkErr != nil {
				t.Skipf("hard links unavailable: %v", hardLinkErr)
			}
			for i := range tc.calls {
				tc.calls[i].ID = fmt.Sprint(i)
			}
			waves, known := l.toolConflictWaves(tc.calls)
			if !known {
				return
			} // Conservative sequential dispatch is safe.
			for _, wave := range waves {
				if len(wave) > 1 {
					t.Fatalf("same resource scheduled concurrently: %v", tc.calls)
				}
			}
		})
	}
}

func TestFileBatchSymlinkAliasesStayOrdered(t *testing.T) {
	l, root := pathScheduleFixture(t)
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	calls := []llm.ToolCall{
		pathScheduleCall("write", "write_file", map[string]any{"path": "real/new.txt", "content": "after"}),
		pathScheduleCall("read", "read_lines", map[string]any{"file": "alias/new.txt"}),
	}
	if waves, known := l.toolConflictWaves(calls); known && len(waves) != 2 {
		t.Fatal("symlink parent allowed write/read overlap")
	}
}

func TestFileBatchIndependentWritesRemainParallel(t *testing.T) {
	l, root := pathScheduleFixture(t)
	calls := []llm.ToolCall{
		pathScheduleCall("a", "write_file", map[string]any{"path": "a.txt", "content": "A"}),
		pathScheduleCall("b", "write_file", map[string]any{"path": filepath.Join(root, "b.txt"), "content": "B"}),
	}
	waves, known := l.toolConflictWaves(calls)
	if !known || len(waves) != 1 || len(waves[0]) != 2 {
		t.Fatal("independent files lost parallel scheduling")
	}
}

func TestFileBatchAliasReadSeesWrittenContent(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%v", thin), func(t *testing.T) {
			l, root := pathScheduleFixture(t)
			calls := []llm.ToolCall{
				pathScheduleCall("write", "write_file", map[string]any{"path": "a.txt", "content": "after\n"}),
				pathScheduleCall("read", "read_lines", map[string]any{"file": filepath.Join(root, "a.txt")}),
			}
			l.registry.MustRegister(NewInvokeTool(l.registry).Spec())
			l.registry.MarkAlwaysOn("invoke_tool")
			deltas := make([]llm.Delta, 0, 2)
			for _, call := range calls {
				if thin {
					call = pathScheduleCall(call.ID, "invoke_tool", map[string]any{"tool": call.Name, "args": json.RawMessage(call.Arguments)})
				}
				c := call
				deltas = append(deltas, llm.Delta{ToolCall: &c})
			}
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{deltas, {{Content: "Finished.", FinishReason: "stop"}}}}
			l.provider, p.calls = p, 0
			l.thinTools = thin
			drainEvents(t, mustRun(t, l, "Write the update and read it back."))
			if len(p.reqs) != 2 {
				t.Fatalf("requests=%d", len(p.reqs))
			}
			var ids []string
			for _, msg := range p.reqs[1] {
				if msg.Role != llm.RoleTool {
					continue
				}
				ids = append(ids, msg.ToolCallID)
				if strings.HasPrefix(msg.Content, "error:") {
					t.Fatalf("tool %s failed: %s", msg.ToolCallID, msg.Content)
				}
				if msg.ToolCallID == "read" && (!strings.Contains(msg.Content, "after") || strings.Contains(msg.Content, "before")) {
					t.Fatalf("stale read: %s", msg.Content)
				}
			}
			if strings.Join(ids, ",") != "write,read" {
				t.Fatalf("result order=%v", ids)
			}
			actual, err := os.ReadFile(filepath.Join(root, "a.txt"))
			if err != nil || string(actual) != "after\n" {
				t.Fatalf("file=%q error=%v", actual, err)
			}
		})
	}
}

func BenchmarkFileBatchPathScheduling(b *testing.B) {
	l, root := pathScheduleFixture(b)
	calls := []llm.ToolCall{
		pathScheduleCall("a", "write_file", map[string]any{"path": "a.txt", "content": "A"}),
		pathScheduleCall("b", "read_lines", map[string]any{"file": filepath.Join(root, "b.txt")}),
		pathScheduleCall("c", "write_file", map[string]any{"path": "c.txt", "content": "C"}),
		pathScheduleCall("d", "read_lines", map[string]any{"file": "d.txt"}),
	}
	b.ReportAllocs()
	for b.Loop() {
		l.toolConflictWaves(calls)
	}
}

// The empty-workspace case is deliberately not treated as the process working
// directory: embedded callers may root their tools elsewhere.
func TestFileBatchWithoutWorkspaceUsesSequentialFallback(t *testing.T) {
	l, _ := pathScheduleFixture(t)
	l.baseDir = ""
	calls := []llm.ToolCall{pathScheduleCall("a", "write_file", map[string]any{"path": "a.txt", "content": "A"}), pathScheduleCall("b", "read_lines", map[string]any{"file": "b.txt"})}
	if _, known := l.toolConflictWaves(calls); known {
		t.Fatal("unresolved workspace was treated as safe for parallel writes")
	}
}

// A move can turn a previously absent destination into an alias of another
// existing hard link. The following write/read wave must use the new identity.
func TestFileBatchRechecksIdentityAfterMove(t *testing.T) {
	l, root := pathScheduleFixture(t)
	if err := os.Link(filepath.Join(root, "a.txt"), filepath.Join(root, "other.txt")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	calls := []llm.ToolCall{
		pathScheduleCall("move", "move", map[string]any{"src": "a.txt", "dest": "moved.txt"}),
		pathScheduleCall("write", "write_file", map[string]any{"path": "moved.txt", "content": "after move\n"}),
		pathScheduleCall("read", "read_lines", map[string]any{"file": "other.txt"}),
	}
	ok, outcomes := l.invokeToolCalls(context.Background(), calls, make(chan Event, 24))
	if !ok || countFailures(outcomes) != 0 {
		t.Fatalf("failed batch: %+v", outcomes)
	}
	if len(l.Messages) != 3 {
		t.Fatalf("results=%d", len(l.Messages))
	}
	for i, msg := range l.Messages {
		if msg.ToolCallID != calls[i].ID {
			t.Fatalf("result order at %d", i)
		}
	}
	if !strings.Contains(l.Messages[2].Content, "after move") {
		t.Fatalf("stale read after move: %s", l.Messages[2].Content)
	}
}

func TestFileBatchPathMetadataIsNotCachedAcrossBatches(t *testing.T) {
	l, root := pathScheduleFixture(t)
	other := filepath.Join(root, "other.txt")
	if err := os.WriteFile(other, []byte("independent"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{
		pathScheduleCall("write", "write_file", map[string]any{"path": "a.txt", "content": "after"}),
		pathScheduleCall("read", "read_lines", map[string]any{"file": "other.txt"}),
	}
	if waves, known := l.toolConflictWaves(calls); !known || len(waves) != 1 {
		t.Fatal("independent files were not parallel")
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "a.txt"), other); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if waves, known := l.toolConflictWaves(calls); known && len(waves) != 2 {
		t.Fatal("stale file identity reused after link replacement")
	}
}

func TestFileBatchFinalSymlinkAndWhitespacePaths(t *testing.T) {
	l, root := pathScheduleFixture(t)
	name := " leading.txt"
	target := filepath.Join(root, name)
	if err := os.WriteFile(target, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	calls := []llm.ToolCall{
		pathScheduleCall("write", "write_file", map[string]any{"path": name, "content": "after"}),
		pathScheduleCall("read", "read_lines", map[string]any{"file": "alias.txt"}),
	}
	if waves, known := l.toolConflictWaves(calls); known && len(waves) != 2 {
		t.Fatal("final symlink to whitespace-prefixed filename was not resolved")
	}
}
