package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func diagnosticFixtureTool(name string) Tool {
	return Tool{Name: name, Description: "Diagnostic fixture.", Schema: "{}", Fn: func(context.Context, json.RawMessage) (Result, error) {
		panic("diagnostics must never execute tools")
	}}
}

func assertDiagnosticParity(t *testing.T, reg *Registry, handle *RegistryDiagnostics) {
	t.Helper()
	want := RegistryDiagnosticCounts{Registered: reg.Len(), Visible: len(reg.VisibleNames())}
	if got := handle.Snapshot(); got != want {
		t.Fatalf("diagnostic counts = %+v, registry = %+v", got, want)
	}
}

func TestRegistryDiagnosticsFollowsExactVisibility(t *testing.T) {
	var absent *Registry
	if absent.Diagnostics() != nil {
		t.Fatal("nil registry acquired diagnostic state")
	}
	reg := NewRegistry()
	reg.MustRegister(diagnosticFixtureTool("first"))
	if reg.diagnostics != nil {
		t.Fatal("ordinary registries must not allocate diagnostic state")
	}
	handle := reg.Diagnostics()
	if handle != reg.Diagnostics() {
		t.Fatal("a registry must reuse its diagnostic handle")
	}
	assertDiagnosticParity(t, reg, handle)
	for _, step := range []struct {
		name  string
		apply func()
	}{
		{"unknown activation", func() { reg.Activate("missing") }},
		{"unknown always-on", func() { reg.MarkAlwaysOn("later") }},
		{"always-on registered later", func() { reg.MustRegister(diagnosticFixtureTool("later")) }},
		{"union overlap", func() { reg.Activate("first", "later"); reg.MarkAlwaysOn("first") }},
		{"idempotent union", func() { reg.Activate("first", "first"); reg.MarkAlwaysOn("first") }},
		{"discovery", func() {
			reg.MustRegister(diagnosticFixtureTool("discovered"))
			reg.ActivateDiscovered("missing", "discovered")
		}},
		{"deactivate always-on and discovered", func() { reg.Deactivate("first", "discovered", "missing") }},
		{"reset", func() { reg.ResetVisibility() }},
		{"read_output registration", func() { reg.EnsureReadOutput() }},
		{"read_output overlap", func() { reg.ActivateDiscovered("read_output"); reg.EnsureReadOutput() }},
		{"register from", func() {
			source := NewRegistry()
			source.MustRegister(diagnosticFixtureTool("copied"))
			if err := reg.RegisterFrom(source, "copied"); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate registration", func() {
			if err := reg.Register(diagnosticFixtureTool("first")); err == nil {
				t.Fatal("duplicate registration succeeded")
			}
		}},
	} {
		t.Run(step.name, func(t *testing.T) {
			step.apply()
			assertDiagnosticParity(t, reg, handle)
		})
	}
	worker := NewRegistrySharingOutputs(reg)
	worker.MustRegister(diagnosticFixtureTool("worker"))
	worker.MarkAlwaysOn("worker")
	if worker.diagnostics != nil || worker.Diagnostics() == handle {
		t.Fatal("sharing outputs must not share diagnostic state")
	}
	assertDiagnosticParity(t, reg, handle)
	assertDiagnosticParity(t, worker, worker.Diagnostics())
}

func TestRegistryDiagnosticsConcurrentPublication(t *testing.T) {
	reg := NewRegistry()
	for i := 0; i < 24; i++ {
		reg.MustRegister(diagnosticFixtureTool(fmt.Sprintf("tool-%d", i)))
	}
	reg.MarkAlwaysOn("tool-0")
	handle := reg.Diagnostics()
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 200; i++ {
				name := fmt.Sprintf("tool-%d", 1+(i+worker)%23)
				reg.ActivateDiscovered(name)
				reg.Deactivate(name)
				if i%20 == 0 {
					reg.ResetVisibility()
				}
			}
		}(worker)
	}
	for reader := 0; reader < 4; reader++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 800; i++ {
				got := handle.Snapshot()
				if got.Registered != 24 || got.Visible < 1 || got.Visible > got.Registered {
					t.Errorf("inconsistent published counts: %+v", got)
					return
				}
			}
		}()
	}
	workers.Wait()
	assertDiagnosticParity(t, reg, handle)
}

func BenchmarkRegistryDiagnosticsVisibility(b *testing.B) {
	for _, observed := range []bool{false, true} {
		name := "nil-handle"
		if observed {
			name = "live-handle"
		}
		b.Run(name, func(b *testing.B) {
			reg := NewRegistry()
			for i := 0; i < 120; i++ {
				reg.MustRegister(diagnosticFixtureTool(fmt.Sprintf("tool-%d", i)))
			}
			if observed {
				reg.Diagnostics()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reg.Activate("tool-60")
				reg.Deactivate("tool-60")
			}
		})
	}
}
