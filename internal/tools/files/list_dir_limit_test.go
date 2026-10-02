package files

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestListDirRequestedLimitPreservesDefaultAndConfiguredCap(t *testing.T) {
	root := treeFixture(t)
	for _, depth := range []int{1, 3} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			tool := NewListDir(root)
			tool.MaxEntries = 7
			registry := core.NewRegistry()
			registry.MustRegister(tool.Spec())
			var baseline string
			for _, limit := range []int{0, 7, 1000000000} {
				args := map[string]any{"depth": depth}
				if limit > 0 {
					args["limit"] = limit
				}
				body, _ := json.Marshal(args)
				result, err := registry.Execute(context.Background(), "list_dir", body)
				if err != nil || result.Err != nil {
					t.Fatalf("valid limit rejected: %v / %v", err, result.Err)
				}
				if limit == 0 {
					baseline = result.Text
				} else if result.Text != baseline {
					t.Fatalf("default order, configured cap or notices changed:\n%s\n%s", baseline, result.Text)
				}
			}
			if tool.MaxEntries != 7 {
				t.Fatal("per-call request mutated the shared configured cap")
			}
		})
	}
}

func TestListDirRequestedLimitMarksIncompleteAndCompleteListings(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		mustWrite(t, filepath.Join(root, name), "test")
	}
	tool := NewListDir(root)
	limited := runListDir(t, tool, `{"limit":2}`).Text
	if !strings.Contains(limited, "a.go (4 bytes)\nb.go (4 bytes)") ||
		strings.Contains(limited, "c.go") || !strings.Contains(limited, "showing first 2 of 3 entries") {
		t.Fatalf("limited listing lost order or truthful scope: %s", limited)
	}
	complete := runListDir(t, tool, `{"limit":3}`).Text
	if !strings.Contains(complete, "c.go") || strings.Contains(complete, "showing first") {
		t.Fatalf("exact complete listing mislabeled: %s", complete)
	}
	if tool.MaxEntries != 500 {
		t.Fatal("request changed subsequent default calls")
	}
}

func TestListDirRequestedTreeLimitIsGlobal(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/one.go", "a/two.go", "b/three.go", "z.go"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, "")
	}
	tool := NewListDir(root)
	requested := runListDir(t, tool, `{"depth":4,"limit":4}`).Text
	configured := NewListDir(root)
	configured.MaxEntries = 4
	baseline := runListDir(t, configured, `{"depth":4}`).Text
	if requested != baseline || !strings.Contains(requested, "listing incomplete") ||
		!strings.Contains(requested, "a/one.go") || strings.Contains(requested, "a/two.go") {
		t.Fatalf("global breadth-first budget changed: %s", requested)
	}
	if full := runListDir(t, tool, `{"depth":4}`).Text; !strings.Contains(full, "b/three.go") || strings.Contains(full, "incomplete") {
		t.Fatal("limited call changed a subsequent full call")
	}
}

type listLimitPersistence struct{ saves, reads int }

func (p *listLimitPersistence) SaveToolOutput(context.Context, string, string) error {
	p.saves++
	return nil
}
func (p *listLimitPersistence) ReadToolOutput(context.Context, string) (string, error) {
	p.reads++
	return "", fmt.Errorf("unexpected read")
}

func TestListDirRequestedLimitAvoidsUnneededRetention(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 150; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("module_%03d_public_api_request_handler_model_fixture.go", i)), "package fixture\n")
	}
	tool := NewListDir(root)
	full := runListDir(t, tool, "{}")
	limited := runListDir(t, tool, `{"limit":20}`)
	if len(full.Text) <= core.ModelOutputInlineBytes || len(limited.Text) >= core.ModelOutputInlineBytes ||
		!strings.Contains(limited.Text, "showing first 20 of 150 entries") {
		t.Fatal("fixture did not exercise truthful inline scope")
	}
	for _, tc := range []struct {
		name     string
		result   Result
		retained bool
	}{{"default", full, true}, {"requested20", limited, false}} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &listLimitPersistence{}
			ctx := core.WithOutputPersistence(context.Background(), backend)
			content := core.NewOutputStore().ModelContentContext(ctx, "list_dir", tc.result)
			if (core.StoredOutputHandle(content) != "") != tc.retained ||
				backend.saves != map[bool]int{false: 0, true: 1}[tc.retained] || backend.reads != 0 {
				t.Fatal("retention contract changed")
			}
			if !tc.retained && content != tc.result.Text {
				t.Fatal("requested entries require another output read")
			}
			t.Logf("raw=%d model=%d retained=%v", len(tc.result.Text), len(content), tc.retained)
		})
	}
}

func TestListDirRequestedLimitKeepsSandboxErrorsAndFilters(t *testing.T) {
	root := treeFixture(t)
	tool := NewListDir(root)
	// A generous requested cap must preserve all prior excluded/depth labels.
	if got, want := runListDir(t, tool, `{"depth":4,"limit":1000000}`).Text, runListDir(t, tool, `{"depth":4}`).Text; got != want {
		t.Fatal("filters or ordering changed")
	}
	for _, raw := range []string{`{"limit":0}`, `{"limit":-1}`, `{"limit":1.5}`} {
		if result, err := tool.Execute(context.Background(), json.RawMessage(raw)); err == nil || result.Err == nil {
			t.Fatalf("invalid limit accepted: %s", raw)
		}
	}
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "outside-secret.go"), "secret")
	args, _ := json.Marshal(map[string]any{"path": outside, "limit": 20, "depth": 4})
	if result, err := tool.Execute(context.Background(), args); err == nil || result.Err == nil || strings.Contains(result.Text, "secret") {
		t.Fatal("requested cap bypassed sandbox")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := tool.Execute(ctx, json.RawMessage(`{"limit":20}`)); err != context.Canceled || result.Err != err {
		t.Fatal("cancellation changed")
	}
}
