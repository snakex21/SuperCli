package agent

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func zipScheduleArchive(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "a.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("archive-value\n")); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func zipScheduleLoop(t *testing.T, root string, specs ...tools.Tool) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	for _, spec := range specs {
		reg.MustRegister(spec)
		reg.Activate(spec.Name)
	}
	reg.MustRegister(NewInvokeTool(reg).Spec())
	l, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, BaseDir: root})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestZipExtractionResourceWaves(t *testing.T) {
	root := zipScheduleArchive(t)
	l := zipScheduleLoop(t, root, tools.NewReadZip(root, 0).Spec(), tools.NewReadLines(root).Spec(), tools.NewPatchFile(root).Spec())
	read := pathScheduleCall("read", "read_lines", map[string]any{"file": "dest/file.txt"})
	for _, target := range []string{"dest", filepath.Join(root, "dest")} {
		extract := pathScheduleCall("zip", "read_zip", map[string]any{"path": "a.zip", "action": "extract", "target_dir": target})
		for _, calls := range [][]llm.ToolCall{{extract, read}, {read, extract}} {
			if waves, known := l.toolConflictWaves(calls); !known || len(waves) != 2 {
				t.Errorf("dependent calls: waves=%d known=%v", len(waves), known)
			}
		}
	}
	for _, args := range []map[string]any{
		{"path": "a.zip", "action": "extract"},
		{"path": "a.zip", "action": "extract", "target_dir": ""},
		{"path": "a.zip", "action": "extract", "target_dir": " \t"},
		{"path": "a.zip", "action": "extract", "target_dir": 42},
		{"path": "a.zip", "action": "extract", "target_dir": nil},
		{"path": "a.zip", "action": 42, "target_dir": "dest"},
		{"path": "a.zip", "action": nil, "target_dir": "dest"},
		{"path": "a.zip", "action": "unknown", "target_dir": "dest"},
	} {
		if _, known := l.toolConflictWaves([]llm.ToolCall{pathScheduleCall("zip", "read_zip", args), read}); known {
			t.Errorf("unknown extraction footprint did not retain the sequential barrier: %v", args)
		}
	}
	patch := pathScheduleCall("patch", "patch_file", map[string]any{"path": "dest/file.txt", "old": "before", "new": "after"})
	for _, action := range []any{nil, "list", ""} {
		args := map[string]any{"path": "a.zip"}
		if action != nil {
			args["action"] = action
		}
		list := pathScheduleCall("zip", "read_zip", args)
		for _, other := range []llm.ToolCall{read, patch} {
			if waves, known := l.toolConflictWaves([]llm.ToolCall{list, other}); !known || len(waves) != 1 {
				t.Errorf("listing lost independent scheduling: waves=%d known=%v", len(waves), known)
			}
		}
	}
	independent := pathScheduleCall("read", "read_lines", map[string]any{"file": "other/file.txt"})
	extract := pathScheduleCall("zip", "read_zip", map[string]any{"path": "a.zip", "action": "extract", "target_dir": "dest"})
	if waves, known := l.toolConflictWaves([]llm.ToolCall{extract, independent}); !known || len(waves) != 1 {
		t.Errorf("unrelated destination lost parallel scheduling: waves=%d known=%v", len(waves), known)
	}
}

func TestZipExtractionDependencyResults(t *testing.T) {
	for _, protocol := range []string{"native", "invoke"} {
		for _, dependent := range []string{"read", "patch", "default"} {
			t.Run(protocol+"/"+dependent, func(t *testing.T) {
				root := zipScheduleArchive(t)
				if err := os.Mkdir(filepath.Join(root, "dest"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "dest/file.txt"), []byte("initial-value\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("probe-value\n"), 0600); err != nil {
					t.Fatal(err)
				}
				zipTool := tools.NewReadZip(root, 0)
				zipTool.ExtractRoot = filepath.Join(root, "custom-extract-root")
				zipSpec := zipTool.Spec()
				zipFn := zipSpec.Fn
				zipFinished, nextDone, early := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
				zipSpec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
					// A concurrent dependent finishes first on the broken scheduler.
					// A correct ordered batch waits here once, then extracts normally.
					select {
					case <-nextDone:
					case <-time.After(20 * time.Millisecond):
					case <-ctx.Done():
						return tools.Result{Err: ctx.Err()}, ctx.Err()
					}
					result, err := zipFn(ctx, args)
					close(zipFinished)
					return result, err
				}
				args := map[string]any{"path": "a.zip", "action": "extract", "target_dir": "dest"}
				nextSpec := tools.NewReadLines(root).Spec()
				call := pathScheduleCall("dependent", "read_lines", map[string]any{"file": "dest/file.txt", "from": 1, "to": 1})
				if dependent == "patch" {
					nextSpec = tools.NewPatchFile(root).Spec()
					call = pathScheduleCall("dependent", "patch_file", map[string]any{"path": "dest/file.txt", "old": "archive-value\n", "new": "patched-value\n"})
				} else if dependent == "default" {
					delete(args, "target_dir")
					call = pathScheduleCall("dependent", "read_lines", map[string]any{"file": "probe.txt", "from": 1, "to": 1})
				}
				nextFn := nextSpec.Fn
				nextSpec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
					select {
					case <-zipFinished:
					default:
						early <- struct{}{}
					}
					result, err := nextFn(ctx, args)
					close(nextDone)
					return result, err
				}
				l := zipScheduleLoop(t, root, zipSpec, nextSpec)
				calls := []llm.ToolCall{pathScheduleCall("zip", "read_zip", args), call}
				if protocol == "invoke" {
					for i, original := range calls {
						calls[i] = pathScheduleCall(original.ID, "invoke_tool", map[string]any{"tool": original.Name, "args": json.RawMessage(original.Arguments)})
					}
					calls = l.resolveInvokeToolCalls(calls)
					if l.InvokeToolDispatches() != 2 || calls[0].Name != "read_zip" || calls[1].Name != call.Name {
						t.Fatal("dispatcher did not resolve exact target calls")
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				ok, outcomes := l.invokeToolCalls(ctx, calls, make(chan Event, 32))
				select {
				case <-early:
					t.Error("dependent ran before extraction finished")
				default:
				}
				if !ok || countFailures(outcomes) != 0 {
					t.Errorf("batch ok=%v failures=%d, want no dependency failure", ok, countFailures(outcomes))
				}
				if len(l.Messages) != 2 || l.Messages[0].ToolCallID != "zip" || l.Messages[1].ToolCallID != "dependent" {
					t.Fatal("tool results changed order or identity")
				}
				wantFile, file := "archive-value\n", filepath.Join(root, "dest/file.txt")
				if dependent == "patch" {
					wantFile = "patched-value\n"
				} else if dependent == "default" {
					entries, err := os.ReadDir(zipTool.ExtractRoot)
					if err != nil || len(entries) != 1 {
						t.Fatalf("default extraction directories=%d err=%v", len(entries), err)
					}
					file = filepath.Join(zipTool.ExtractRoot, entries[0].Name(), "file.txt")
					if !strings.Contains(l.Messages[1].Content, "probe-value") {
						t.Error("default barrier read result differs")
					}
				} else if !strings.Contains(l.Messages[1].Content, "archive-value") || strings.Contains(l.Messages[1].Content, "initial-value") {
					t.Error("dependent read returned stale evidence")
				}
				data, err := os.ReadFile(file)
				if err != nil || string(data) != wantFile {
					t.Errorf("final extraction/edit state mismatch: readErr=%v matches=%v", err, string(data) == wantFile)
				}
			})
		}
	}
}
