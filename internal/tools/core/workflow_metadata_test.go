package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWorkflowMetadataRemainsImmutableAcrossRegistries(t *testing.T) {
	reg := NewRegistry()
	names := []string{"next"}
	reg.MustRegister(Tool{Name: "parent", Description: "fixture", NextTools: names, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	reg.MarkAlwaysOn("parent")
	names[0] = "injected"
	tool, _ := reg.Get("parent")
	tool.NextTools[0] = "injected"
	visible := reg.Visible()
	visible[0].NextTools[0] = "injected"
	copy := NewRegistry()
	if err := copy.RegisterFrom(reg, "parent"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*Registry{reg, copy} {
		got, _ := r.Get("parent")
		if got.NextTools[0] != "next" {
			t.Fatal("caller could mutate registered capabilities")
		}
	}
}
