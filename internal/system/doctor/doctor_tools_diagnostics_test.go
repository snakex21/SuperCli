package doctor

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/tools"
)

func TestToolsDiagnosticHandlePreservesDoctorCheck(t *testing.T) {
	if got := toolsCheckWithDiagnostics(nil, nil); got != toolsCheck(nil) || got.Status != Warn || got.Detail != "registry not wired" {
		t.Fatalf("unwired diagnostics: %+v", got)
	}
	reg := tools.NewRegistry()
	handle := reg.Diagnostics()
	assertParity := func() {
		t.Helper()
		if got, want := toolsCheckWithDiagnostics(nil, handle), toolsCheck(reg); got != want {
			t.Fatalf("diagnostic check = %+v, executable registry = %+v", got, want)
		}
	}
	assertParity()
	reg.MustRegister(tools.Tool{Name: "lazy", Description: "A lazy fixture.", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		t.Fatal("diagnostic check executed a lazy tool")
		return tools.Result{}, nil
	}})
	assertParity()
	reg.ActivateDiscovered("lazy")
	assertParity()
	reg.ResetVisibility()
	assertParity()
	reg.EnsureReadOutput()
	assertParity()
	// After the caller releases its executable registry, counts stay available.
	want := toolsCheck(reg)
	reg = nil
	if got := toolsCheckWithDiagnostics(nil, handle); got != want {
		t.Fatalf("completed-run diagnostics = %+v, want %+v", got, want)
	}
}
