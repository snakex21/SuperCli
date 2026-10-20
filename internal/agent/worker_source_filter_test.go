package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestWorkerSourceFilteredRowsMatchGeneric(t *testing.T) {
	root := t.TempDir()
	for _, call := range []llm.ToolCall{
		{Name: "read_lines", Arguments: `{"file":"keep.go"}`},
		{Name: "read_context", Arguments: `{"file":"keep.go"}`},
		{Name: "read_many"},
		{Name: "search_code"},
		{Name: "read_output"},
		{Name: invokeToolName, Arguments: `{"tool":"read_lines","args":{"file":"keep.go"}}`},
	} {
		for _, content := range []string{
			"1 | first\n2 | ź🙂\n3 | ... clipped\n4 | last\n",
			"== [1] keep.go:1-2 ==\n1 | package keep\n2 | keep value\n== [2] reject.go:1-2 ==\n1 | package reject\n2 | reject value\n== [3] keep.go:3-3 ==\n3 | another\n",
			"keep.go:1:package keep\nreject.go:2:reject value\nkeep.go:3:another\n",
			"error: cannot read\n1 | unavailable\n",
		} {
			generic := sourceRows(call, content, root)
			if got := sourceRowsFiltered(call, content, root, func(string) bool { return true }); !reflect.DeepEqual(got, generic) {
				t.Fatalf("accept-all changed parser output for %s: %#v / %#v", call.Name, got, generic)
			}
			keep := sourcePath("keep.go", root)
			var want []sourceRow
			for _, row := range generic {
				if row.location.path == keep {
					want = append(want, row)
				}
			}
			calls := make(map[string]int)
			got := sourceRowsFiltered(call, content, root, func(path string) bool {
				calls[path]++
				return path == keep
			})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("filtered rows differ for %s: %#v / %#v", call.Name, got, want)
			}
			for path, count := range calls {
				if count != 1 {
					t.Fatalf("path %q selected %d times", path, count)
				}
			}
		}
	}
	rows := sourceRowsFiltered(llm.ToolCall{Name: "read_lines", Arguments: `{"file":"keep.go"}`}, "1 | ź🙂\r\n", root, func(string) bool { return true })
	if len(rows) != 1 || rows[0].hash != sha256.Sum256([]byte("ź🙂")) || rows[0].start != 0 || rows[0].end != len("1 | ź🙂\r\n") {
		t.Fatalf("hash or source offsets changed: %#v", rows)
	}
}

func TestWorkerSourceFilteredRowsPreserveInspectionBudget(t *testing.T) {
	var content strings.Builder
	content.WriteString("== [1] reject.go:1-2000 ==\n")
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&content, "%d | rejected source\n", i)
	}
	content.WriteString("== [2] keep.go:1-1 ==\n1 | beyond original budget\n")
	calls := 0
	got := sourceRowsFiltered(llm.ToolCall{Name: "read_many"}, content.String(), t.TempDir(), func(path string) bool {
		calls++
		return strings.HasSuffix(path, "keep.go")
	})
	if len(got) != 0 || calls != 1 {
		t.Fatalf("filter bypassed original 2000-row budget: %d rows, %d paths", len(got), calls)
	}
}

func TestWorkerSourceEligibilityRemainsPerHandoff(t *testing.T) {
	a, root := workerSourceFixture(t)
	ctx := context.Background()
	if got := a.workerSourceContext(ctx, "unmentioned.go", SubAgent{}); got != "" {
		t.Fatal("irrelevant source transferred")
	}
	if got := a.workerSourceContext(ctx, "cache/store.go", SubAgent{}); !strings.Contains(got, "expiry = 100") {
		t.Fatal("negative path eligibility leaked into next task")
	}
	if got := a.workerSourceContext(ctx, "cache/store.go", SubAgent{AllowedTools: []string{"web_fetch"}}); got != "" {
		t.Fatal("positive eligibility bypassed worker restrictions")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got := a.workerSourceContext(canceled, "cache/store.go", SubAgent{}); got != "" {
		t.Fatal("canceled handoff returned evidence")
	}
	if err := os.WriteFile(filepath.Join(root, "cache/store.go"), []byte("package cache\nconst expiry = 200\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.workerSourceContext(ctx, "cache/store.go", SubAgent{}); got != "" {
		t.Fatal("positive eligibility bypassed disk freshness")
	}
}

func TestWorkerSourceFilteredFileLimitAndNewestEvidence(t *testing.T) {
	a, root := workerSourceFixture(t)
	a.ParentLoop.Messages = nil
	var task, content strings.Builder
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("file%d.go", i)
		task.WriteString(name + " ")
		if err := os.WriteFile(filepath.Join(root, name), []byte("current value\n"), 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&content, "== [%d] %s:1-1 ==\n1 | current value\n", i+1, name)
	}
	call := llm.ToolCall{ID: "new", Name: "read_many"}
	a.ParentLoop.Messages = []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Name: "read_many"}}},
		{Role: llm.RoleTool, ToolCallID: "old", Content: "== [1] file0.go:1-1 ==\n1 | stale value\n"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
		{Role: llm.RoleTool, ToolCallID: call.ID, Content: content.String()},
	}
	got := a.workerSourceContext(context.Background(), task.String(), SubAgent{})
	for i := 0; i < 4; i++ {
		if !strings.Contains(got, fmt.Sprintf("file%d.go", i)) {
			t.Fatalf("lost eligible file %d or newest hash: %s", i, got)
		}
	}
	if strings.Contains(got, "file4.go") || strings.Contains(got, "stale value") {
		t.Fatalf("file limit or newest evidence changed: %s", got)
	}
	if err := a.ParentLoop.HideRange(2, 4); err != nil {
		t.Fatal(err)
	}
	if got := a.workerSourceContext(context.Background(), task.String(), SubAgent{}); got != "" {
		t.Fatal("hidden current evidence or stale older evidence transferred")
	}
}
