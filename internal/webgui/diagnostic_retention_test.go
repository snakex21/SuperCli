package webgui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"weak"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

func writeDiagnosticOrchestratorFixture(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "config.toml"), []byte("orchestrator = true\npreflight_repo = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The real builder wires task.Fn as AgentTool.execute with ParentLoop. Keeping
// that executable registry recreates the former Engine retention path without
// copying the builder or depending on process RSS or finalizers in a GC cycle.
func diagnosticRetentionFixture(t *testing.T, legacy bool) (*Engine, *tools.Registry, weak.Pointer[agent.Loop]) {
	t.Helper()
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	loop, reg, err := eng.buildLoopWithSession(nil, nil, eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("task"); !ok {
		t.Fatal("fixture requires the real parent task callback")
	}
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("synthetic history ", 1<<20)})
	reg.ModelResultContent("fixture-output", tools.Result{Text: strings.Repeat("synthetic tool output ", 1<<18)})
	ref := weak.Make(loop)
	if legacy {
		return eng, reg, ref
	}
	return eng, nil, ref
}

func TestEngineDiagnosticHandleReleasesPreviousLoop(t *testing.T) {
	eng, _, ref := diagnosticRetentionFixture(t, false)
	diagnostics := eng.toolDiagnostics
	want := diagnostics.Snapshot()
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	if ref.Value() != nil {
		t.Fatal("GUI diagnostic state retained task.Fn -> ParentLoop -> history")
	}
	if got := diagnostics.Snapshot(); got != want || got.Registered == 0 {
		t.Fatalf("finished-run diagnostic counts lost: got %+v, want %+v", got, want)
	}
	runtime.KeepAlive(eng)
}

func TestLegacyDiagnosticRegistryRetainsPreviousLoop(t *testing.T) {
	eng, legacy, ref := diagnosticRetentionFixture(t, true)
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	if ref.Value() == nil {
		t.Fatal("legacy control did not retain the real parent task loop")
	}
	runtime.KeepAlive(legacy)
	runtime.KeepAlive(eng)
}

func TestEngineDiagnosticHandleTracksBaseRegistryDuringAndAfterWork(t *testing.T) {
	for _, orchestrator := range []bool{false, true} {
		name := "adaptive"
		if orchestrator {
			name = "orchestrator"
		}
		t.Run(name, func(t *testing.T) {
			eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			if orchestrator {
				writeDiagnosticOrchestratorFixture(t, eng.DataDir())
			}
			loop, base, err := eng.buildLoopWithSession(nil, nil, eng.Home(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertParity := func() {
				t.Helper()
				want := tools.RegistryDiagnosticCounts{Registered: base.Len(), Visible: len(base.VisibleNames())}
				if got := eng.toolDiagnostics.Snapshot(); got != want {
					t.Fatalf("base diagnostics = %+v, want %+v", got, want)
				}
			}
			assertParity()
			if orchestrator && len(loop.VisibleToolNames()) == len(base.VisibleNames()) {
				t.Fatal("fixture did not restrict the parent registry")
			}
			base.ActivateDiscovered("read_lines", "send_message")
			assertParity()
			base.ResetVisibility()
			assertParity()
			// Exercise a complete deterministic run with the original callbacks.
			ch, err := loop.Run(t.Context(), "hello")
			if err != nil {
				t.Fatal(err)
			}
			for event := range ch {
				if failure, ok := event.(agent.ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			assertParity()
		})
	}
}

func TestEngineDiagnosticsLeavesDelegationExecutable(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	_, base, err := eng.buildLoopWithSession(nil, nil, eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := eng.toolDiagnostics.Snapshot()
	task, ok := base.Get("task")
	if !ok {
		t.Fatal("task missing")
	}
	result, err := task.Fn(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"Reply with ready."}`))
	if err != nil || result.Err != nil || result.Text == "" {
		t.Fatalf("real delegation callback: error=%v, result=%+v", err, result)
	}
	if len(eng.workers.List()) != 1 {
		t.Fatal("delegation did not keep its worker")
	}
	after := eng.toolDiagnostics.Snapshot()
	if after.Registered != before.Registered || after.Visible != len(base.VisibleNames()) {
		t.Fatalf("diagnostics changed tool availability: before=%+v after=%+v", before, after)
	}
}
