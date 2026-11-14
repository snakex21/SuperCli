package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// The TUI's late-registered file tools must run through code-intel
// invalidation after a successful edit, exactly like the web and batch
// surfaces. A spy stands in for CodeIntel.WrapMutation and records every
// mutation that completed through the wrapped tool.
func TestInteractiveToolset_LateFileToolsInvalidateCodeIntel(t *testing.T) {
	home, dataDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	var checkpointed []string
	ts := newInteractiveToolset(registry, home, dataDir, config.TomlConfig{}, func(spec tools.Tool) tools.Tool {
		checkpointed = append(checkpointed, spec.Name)
		return spec
	})
	t.Cleanup(func() {
		ts.CodeIntel.Close()
		ts.ProcessSession.Close()
		ts.FTSClose()
	})

	var mu sync.Mutex
	var invalidated []string
	wrapped := map[string]bool{}
	ts.deps.CodeIntelWrap = func(spec tools.Tool) tools.Tool {
		wrapped[spec.Name] = true
		original := spec.Fn
		name := spec.Name
		spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			res, err := original(ctx, args)
			if err == nil && res.Err == nil {
				mu.Lock()
				invalidated = append(invalidated, name)
				mu.Unlock()
			}
			return res, err
		}
		return spec
	}
	ts.registerLate()

	for _, name := range []string{"patch_file", "create_file", "write_file", "make_dir", "move", "copy", "trash", "web_download"} {
		if !wrapped[name] {
			t.Errorf("%s is not wrapped for code-intel invalidation in the TUI", name)
		}
	}

	calls := []struct{ tool, args string }{
		{"patch_file", `{"path":"main.go","old":"func main() {}","new":"func main() { println(1) }"}`},
		{"write_file", `{"path":"other.go","content":"package main\n"}`},
	}
	for _, c := range calls {
		res, err := registry.Execute(context.Background(), c.tool, json.RawMessage(c.args))
		if err != nil || res.Err != nil {
			t.Fatalf("%s failed: result=%+v err=%v", c.tool, res, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(invalidated) != 2 || invalidated[0] != "patch_file" || invalidated[1] != "write_file" {
		t.Fatalf("code-intel invalidation calls = %v, want [patch_file write_file]", invalidated)
	}
	got, err := os.ReadFile(filepath.Join(home, "main.go"))
	if err != nil || string(got) != "package main\n\nfunc main() { println(1) }\n" {
		t.Fatalf("patch_file did not edit the file: %q %v", got, err)
	}
	// Checkpoint snapshots still wrap the same tools (inner wrapper).
	seen := map[string]bool{}
	for _, name := range checkpointed {
		seen[name] = true
	}
	for _, name := range []string{"patch_file", "write_file"} {
		if !seen[name] {
			t.Errorf("%s lost its checkpoint wrapper", name)
		}
	}
}
