package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRegistryDiscoveryExcludesAutomaticPromotions(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"automatic", "explicit", "always"} {
		r.MustRegister(Tool{Name: name, Description: name, Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	}
	r.MarkAlwaysOn("always")
	r.Activate("automatic")
	r.ActivateDiscovered("explicit", "absent", "explicit")
	if got := r.DiscoveredNames(); !reflect.DeepEqual(got, []string{"explicit"}) {
		t.Fatalf("discoveries=%v", got)
	}
	if !r.IsActive("automatic") || !r.IsActive("explicit") || r.IsActive("absent") {
		t.Fatal("activation changed or missing tool restored")
	}
	r.Deactivate("explicit")
	if len(r.DiscoveredNames()) != 0 {
		t.Fatal("deactivated discovery retained")
	}
	r.ActivateDiscovered("explicit")
	r.ResetVisibility()
	if len(r.DiscoveredNames()) != 0 || r.IsActive("explicit") || !r.IsVisible("always") {
		t.Fatal("reset lost always-on state or retained discovery")
	}
}
