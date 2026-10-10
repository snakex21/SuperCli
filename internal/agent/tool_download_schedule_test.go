package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/sandbox"
)

func TestDownloadAndFileReadStayOrdered(t *testing.T) {
	l, _ := pathScheduleFixture(t)
	l.registry.MustRegister(tools.Tool{
		Name: "web_download", Description: "Download a test asset", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	download := pathScheduleCall("download", "web_download", map[string]any{"url": "https://example.com/a.png", "path": "assets/a.png"})
	read := pathScheduleCall("read", "read_lines", map[string]any{"file": "assets/a.png"})
	waves, known := l.toolConflictWaves([]llm.ToolCall{download, read})
	if !known || len(waves) != 2 {
		t.Fatal("download and dependent file read must be ordered")
	}
	second := pathScheduleCall("second", "web_download", map[string]any{"url": "https://example.com/b.png", "path": "assets/b.png"})
	waves, known = l.toolConflictWaves([]llm.ToolCall{download, second})
	if !known || len(waves) != 1 || len(waves[0]) != 2 {
		t.Fatal("independent downloads must retain parallel scheduling")
	}
	if toolKind("web_download") != "mutation" {
		t.Fatal("successful downloads must invalidate stale workspace observations")
	}
}

func TestExportDownloadsUseInvocationContextForScheduling(t *testing.T) {
	l, _ := pathScheduleFixture(t)
	l.registry.MustRegister(tools.Tool{Name: "web_download", Description: "Download test asset", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	export := t.TempDir()
	ctx := sandbox.WithDownloadExportDirs(context.Background(), export)
	first := pathScheduleCall("first", "web_download", map[string]any{"url": "https://example.com/a.gif", "path": filepath.Join(export, "a.gif")})
	second := pathScheduleCall("second", "web_download", map[string]any{"url": "https://example.com/b.gif", "path": filepath.Join(export, "b.gif")})
	if waves, known := l.toolConflictWavesContext(ctx, []llm.ToolCall{first, second}); !known || len(waves) != 1 || len(waves[0]) != 2 {
		t.Fatal("independent authorized exports lost parallel scheduling")
	}
	if _, known := l.toolConflictWaves([]llm.ToolCall{first, second}); known {
		t.Fatal("export grant leaked into a different invocation")
	}
	alias := pathScheduleCall("alias", "web_download", map[string]any{"url": "https://example.com/a.gif", "path": filepath.Join(export, "nested", "..", "a.gif")})
	if waves, known := l.toolConflictWavesContext(ctx, []llm.ToolCall{first, alias}); !known || len(waves) != 2 {
		t.Fatal("two downloads to one canonical destination must remain ordered")
	}
	write := pathScheduleCall("write", "write_file", map[string]any{"path": filepath.Join(export, "a.gif"), "content": "data"})
	if _, known := l.toolConflictWavesContext(ctx, []llm.ToolCall{first, write}); known {
		t.Fatal("a non-download tool borrowed export authorization from the shared path cache")
	}
	outside := pathScheduleCall("outside", "web_download", map[string]any{"url": "https://example.com/c.gif", "path": filepath.Join(t.TempDir(), "c.gif")})
	if _, known := l.toolConflictWavesContext(ctx, []llm.ToolCall{first, outside}); known {
		t.Fatal("a destination outside the current grant was admitted for parallel execution")
	}
}

// A completion barrier makes overlap observable without timing assumptions.
// Before context-aware scheduling, the first export waits alone until canceled.
func TestExportDownloadBatchRunsTogetherAndPreservesResultOrder(t *testing.T) {
	l, _ := pathScheduleFixture(t)
	export := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = sandbox.WithDownloadExportDirs(ctx, export)
	started := atomic.Int32{}
	ready := make(chan struct{})
	l.registry.MustRegister(tools.Tool{
		Name: "web_download", Description: "Download test asset", Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
		Fn: func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return tools.Result{Err: err}, nil
			}
			if _, err := sandbox.ResolveDownloadDestination(ctx, l.baseDir, args.Path); err != nil {
				return tools.Result{Err: err}, nil
			}
			if started.Add(1) == 2 {
				close(ready)
			}
			select {
			case <-ready:
				return tools.Result{Text: filepath.Base(args.Path)}, nil
			case <-ctx.Done():
				return tools.Result{Err: ctx.Err()}, nil
			}
		},
	})
	l.registry.MarkAlwaysOn("web_download")
	calls := []llm.ToolCall{
		pathScheduleCall("first", "web_download", map[string]any{"path": filepath.Join(export, "a.gif")}),
		pathScheduleCall("second", "web_download", map[string]any{"path": filepath.Join(export, "b.gif")}),
	}
	if ok, outcomes := l.invokeToolCalls(ctx, calls, make(chan Event, 16)); !ok || len(outcomes) != 2 || outcomes[0].failed || outcomes[1].failed {
		t.Fatalf("authorized export batch failed: ok=%t outcomes=%+v", ok, outcomes)
	}
	var ids []string
	for _, message := range l.Messages {
		if message.Role == llm.RoleTool {
			ids = append(ids, message.ToolCallID)
		}
	}
	if strings.Join(ids, ",") != "first,second" || started.Load() != 2 {
		t.Fatalf("batch result order=%v starts=%d", ids, started.Load())
	}
}
