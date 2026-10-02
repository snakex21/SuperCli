package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryRevisionTracksContractChanges(t *testing.T) {
	r := NewRegistry()
	noop := func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "ok"}, nil }
	spec := Tool{Name: "record", Description: "Read a record.", Schema: "{}", Fn: noop}
	for _, change := range []struct {
		name    string
		changed bool
		apply   func()
	}{
		{"register", true, func() { r.MustRegister(spec) }},
		{"duplicate register", false, func() {
			if r.Register(spec) == nil {
				t.Fatal("duplicate accepted")
			}
		}},
		{"invalid register", false, func() {
			if r.Register(Tool{}) == nil {
				t.Fatal("invalid accepted")
			}
		}},
		{"missing activate", false, func() { r.Activate("absent") }},
		{"always on", true, func() { r.MarkAlwaysOn("record") }},
		{"always on repeat", false, func() { r.MarkAlwaysOn("record") }},
		{"activate always on", true, func() { r.Activate("record") }},
		{"activate repeat", false, func() { r.Activate("record", "record") }},
		{"discover active", true, func() { r.ActivateDiscovered("record") }},
		{"discover repeat and absent", false, func() { r.ActivateDiscovered("record", "absent", "record") }},
		{"deactivate", true, func() { r.Deactivate("record") }},
		{"deactivate absent", false, func() { r.Deactivate("record", "absent") }},
		{"empty reset", false, func() { r.ResetVisibility() }},
		{"discover dormant", true, func() { r.ActivateDiscovered("record") }},
		{"reset", true, func() { r.ResetVisibility() }},
		{"reset repeat", false, func() { r.ResetVisibility() }},
		{"install output", true, func() { r.EnsureReadOutput() }},
		{"install output repeat", false, func() { r.EnsureReadOutput() }},
		{"execution", false, func() {
			if _, err := r.Execute(context.Background(), "record", json.RawMessage("{}")); err != nil {
				t.Fatal(err)
			}
		}},
		{"output retention", false, func() { r.CompactModelOutput("record", "small result") }},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := r.Revision()
			change.apply()
			after := r.Revision()
			if (after > before) != change.changed || after < before {
				t.Fatalf("revision=%d -> %d; changed=%v", before, after, change.changed)
			}
		})
	}
}

func TestRegistryRevisionDescriptorsAreValues(t *testing.T) {
	r := NewRegistry()
	template := Tool{Name: "record", Description: "Original.", Schema: "{}", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }}
	r.MustRegister(template)
	r.MarkAlwaysOn("record")
	before := r.Revision()
	template.Description = "Changed externally."
	fromGet, _ := r.Get("record")
	fromGet.Schema = "changed"
	visible := r.Visible()
	visible[0].Name = "changed"
	registered, _ := r.Get("record")
	if registered.Description != "Original." || registered.Schema != "{}" || registered.Name != "record" || r.Revision() != before {
		t.Fatal("external value changed registered contract")
	}
	child := NewRegistrySharingOutputs(r)
	if err := child.RegisterFrom(r, "record"); err != nil {
		t.Fatal(err)
	}
	if child.Revision() == 0 || r.Revision() != before {
		t.Fatal("copied registry revision leaked")
	}
	child.EnsureReadOutput()
	childRevision := child.Revision()
	child.Deactivate("record")
	if child.Revision() != childRevision {
		t.Fatal("absent child activation invalidated")
	}
}
