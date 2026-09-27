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

// The saved GunMayhem overview had the project directory between expanded
// log/backup trees. Sorting the whole tree hid that root in the model's bounded
// head/tail preview even though breadth-first traversal had found it.
func TestDirectoryOverviewKeepsProjectRootInModelPreview(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%t", thin), func(t *testing.T) {
			root := t.TempDir()
			write := func(name string) {
				t.Helper()
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range []string{".playwright-mcp", "z-backups"} {
				for i := 0; i < 100; i++ {
					write(fmt.Sprintf("%s/console-%03d-%s.log", dir, i, strings.Repeat("x", 70)))
				}
			}
			write("gunmayhem-go/go.mod")
			write("gunmayhem-go/tmp/network.log")

			reg := tools.NewRegistry()
			spec := tools.NewListDir(root).Spec()
			execute, lists := spec.Fn, 0
			var full string
			spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				lists++
				result, err := execute(ctx, raw)
				full = result.Text
				return result, err
			}
			reg.MustRegister(spec)
			reg.MarkAlwaysOn("list_dir")
			calls := [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "overview", Name: "list_dir", Arguments: "{\"path\":\".\",\"depth\":2}"}}},
				{{Content: "The project is in gunmayhem-go.", FinishReason: "stop"}},
			}
			if thin {
				calls[0] = []llm.Delta{{Content: "«list_dir\npath: .\ndepth: 2\n»", FinishReason: "stop"}}
			}
			provider := &outputReplayProvider{stubProvider: &stubProvider{name: "directory-overview", scripts: calls}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, ThinTools: thin, StableToolset: true, CatalogHoist: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Inspect the workspace directory structure.")) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if lists != 1 || len(provider.reqs) != 2 {
				t.Fatalf("listings=%d model requests=%d", lists, len(provider.reqs))
			}
			var preview string
			for _, m := range provider.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "list_dir" {
					preview = m.Content
				}
			}
			if !strings.Contains(preview, "[large tool output:") {
				t.Fatal("fixture did not exercise bounded model output")
			}
			if !strings.Contains(preview, "\ngunmayhem-go/\n") {
				t.Fatalf("project root found by list_dir is hidden in model preview:\n%s", preview)
			}
			if !strings.Contains(full, "gunmayhem-go/tmp/ (depth limit)") ||
				!strings.Contains(full, "console-099-") ||
				strings.Contains(full, "network.log") {
				t.Fatal("complete result lost entries or exceeded requested depth")
			}
			t.Logf("one listing; full=%d bytes preview=%d bytes; project root visible", len(full), len(preview))
		})
	}
}
