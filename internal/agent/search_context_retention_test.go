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

func TestCappedSearchLocationsRecoverWithoutRescan(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "matches.txt")
			var source strings.Builder
			for i := 0; i < 20; i++ {
				source.WriteString(strings.Repeat("before\n", 20))
				fmt.Fprintf(&source, "needle_%02d=6842\n", i)
				source.WriteString(strings.Repeat("after\n", 40))
			}
			if err := os.WriteFile(path, []byte(source.String()), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			spec := tools.NewSearchCode(root).Spec()
			execute := spec.Fn
			searches := 0
			spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				searches++
				result, err := execute(ctx, args)
				if err == nil && result.Err == nil {
					if !strings.Contains(result.Text, "context capped at 500 lines") || strings.Contains(result.Text, "needle_19=6842") {
						t.Fatal("fixture did not hide the last location behind the context cap")
					}
					// Captured results must remain inspectable after the source goes away.
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				return result, err
			}
			reg.MustRegister(spec)
			reg.MarkAlwaysOn("search_code")
			calls := [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "search", Name: "search_code", Arguments: `{"query":"needle_","context":20,"max":21}`}}},
				{{ToolCall: &llm.ToolCall{ID: "recover", Name: "read_output", Arguments: `{"handle":"out_000001","query":"needle_19=6842"}`}}},
				{{Content: "Recovered the captured final location.", FinishReason: "stop"}},
			}
			if thin {
				calls[0] = []llm.Delta{{Content: "«search_code\nquery: needle_\ncontext: 20\nmax: 21\n»", FinishReason: "stop"}}
				calls[1] = []llm.Delta{{Content: "«read_output\nhandle: out_000001\nquery: needle_19=6842\n»", FinishReason: "stop"}}
			}
			provider := &outputReplayProvider{stubProvider: &stubProvider{name: "retained-search", scripts: calls}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, ThinTools: thin, MaxSteps: 4})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Find the numbered settings and inspect the final captured location.")) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if searches != 1 || len(provider.reqs) != 3 {
				t.Fatalf("searches=%d requests=%d", searches, len(provider.reqs))
			}
			found := false
			for _, m := range provider.reqs[2] {
				if m.Role == llm.RoleTool && m.Name == "read_output" && strings.Contains(m.Content, "matches.txt:1180:needle_19=6842") {
					found = true
				}
			}
			if !found {
				t.Fatal("location already found by the original search was lost from retained output")
			}
			t.Log("one search, one retained-output read, no source file remaining")
		})
	}
}
