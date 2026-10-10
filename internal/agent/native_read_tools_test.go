package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const nativeReadTestSchema = `{"type":"object","properties":{"ref":{"type":"string"}},"required":["ref"]}`

func nativeReadTestTool(name string) tools.Tool {
	return tools.Tool{
		Name: name, Description: "Exact contract for " + name, Schema: nativeReadTestSchema, ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "fixture"}, nil
		},
	}
}

func nativeReadTestNames(defs []llm.ToolDef) []string {
	var names []string
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func TestNativeReadToolsSelectOnlyRegisteredCoreWorkflowAndPreserveRegistry(t *testing.T) {
	var specs []tools.Tool
	for _, name := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "zulu", "yankee"} {
		specs = append(specs, nativeReadTestTool(name))
	}
	source := nativeReadTestTool("core_source")
	source.ReadOnly = false
	source.NextTools = []string{"zulu", "missing_target", "yankee", "zulu"}
	specs = append(specs, source)
	// An unrelated read tool and non-core workflow do not become native.
	other := nativeReadTestTool("other_source")
	other.ReadOnly = false
	other.NextTools = []string{"hotel"}
	specs = append(specs, other)
	core := func(name string) bool { return name == source.Name }
	var baseline []llm.ToolDef
	for _, reverse := range []bool{false, true} {
		reg := tools.NewRegistry()
		for i := range specs {
			at := i
			if reverse {
				at = len(specs) - 1 - i
			}
			reg.MustRegister(specs[at])
		}
		reg.MarkAlwaysOn(source.Name)
		reg.ActivateDiscovered("golf")
		names, visible := reg.Names(), reg.VisibleNames()
		sort.Strings(names)
		active, discovered, revision := reg.ActiveNames(), reg.DiscoveredNames(), reg.Revision()
		got := selectNativeReadTools(reg, core)
		wantNames := []string{"yankee", "zulu"}
		if !reflect.DeepEqual(nativeReadTestNames(got), wantNames) {
			t.Fatalf("selected %v, want %v", nativeReadTestNames(got), wantNames)
		}
		if baseline == nil {
			baseline = got
		} else if !reflect.DeepEqual(got, baseline) {
			t.Fatal("registration order changed the native schema snapshot")
		}
		afterNames := reg.Names()
		sort.Strings(afterNames)
		if !reflect.DeepEqual(afterNames, names) || !reflect.DeepEqual(reg.VisibleNames(), visible) ||
			!reflect.DeepEqual(reg.ActiveNames(), active) || !reflect.DeepEqual(reg.DiscoveredNames(), discovered) || reg.Revision() != revision {
			t.Fatal("selection changed registry state")
		}
		for _, def := range got {
			registered, _ := reg.Get(def.Name)
			if def.Description != registered.Description || def.Schema != registered.Schema {
				t.Fatalf("contract was rewritten: %+v", def)
			}
		}
		got[0].Description = "caller changed its snapshot"
		registered, _ := reg.Get("yankee")
		if registered.Description == got[0].Description {
			t.Fatal("snapshot shares mutable descriptors with the registry")
		}
		// Preserve the comparison snapshot rather than the caller's altered copy.
		baseline = selectNativeReadTools(reg, core)
	}
}

func TestNativeReadToolsRespectExistingEligibilityAndPropertyBound(t *testing.T) {
	reg := tools.NewRegistry()
	for _, tc := range []struct {
		name, schema string
		readOnly     bool
	}{
		{"allowed", nativeReadTestSchema, true},
		{"compact", `{"ref":{"type":"string"},"fresh":{"type":"boolean"}}`, true},
		{"mutator", nativeReadTestSchema, false},
		{"nested", `{"type":"object","properties":{"options":{"type":"object"}}}`, true},
		{"array", `{"type":"object","properties":{"refs":{"type":"array"}}}`, true},
		{"union", `{"type":"object","properties":{"ref":{"oneOf":[{"type":"string"},{"type":"number"}]}}}`, true},
		{invokeToolName, nativeReadTestSchema, true},
		{"tool_search", nativeReadTestSchema, true},
		{"core_reader", nativeReadTestSchema, true},
	} {
		spec := nativeReadTestTool(tc.name)
		spec.Schema, spec.ReadOnly = tc.schema, tc.readOnly
		if tc.name == "core_reader" {
			spec.NextTools = []string{"allowed", "compact", "mutator", "nested", "array", "union", invokeToolName, "tool_search", "core_reader", "properties_8", "properties_9"}
		}
		reg.MustRegister(spec)
	}
	for _, count := range []int{8, 9} {
		props := make(map[string]any)
		for i := 0; i < count; i++ {
			props[fmt.Sprintf("p%d", i)] = map[string]string{"type": "integer"}
		}
		raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props})
		spec := nativeReadTestTool(fmt.Sprintf("properties_%d", count))
		spec.Schema = string(raw)
		reg.MustRegister(spec)
	}
	got := nativeReadTestNames(selectNativeReadTools(reg, func(name string) bool { return name == "core_reader" }))
	want := []string{"allowed", "compact", "properties_8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
}

func TestNativeReadToolsBoundBytesAndSkipOversizedCandidates(t *testing.T) {
	for _, exact := range []bool{false, true} {
		reg := tools.NewRegistry()
		source := nativeReadTestTool("core_source")
		source.NextTools = []string{"large", "small"}
		reg.MustRegister(source)
		large := nativeReadTestTool("large")
		length := nativeReadToolBytes - len(large.Name) - len(large.Schema)
		if !exact {
			length++
		}
		large.Description = strings.Repeat("x", length)
		reg.MustRegister(large)
		reg.MustRegister(nativeReadTestTool("small"))
		got := selectNativeReadTools(reg, func(name string) bool { return name == source.Name })
		want := "small"
		if exact {
			want = "large"
		}
		if names := nativeReadTestNames(got); !reflect.DeepEqual(names, []string{want}) {
			t.Fatalf("exact=%v: selected %v, want %s", exact, names, want)
		}
		used := 0
		for _, def := range got {
			used += len(def.Name) + len(def.Description) + len(def.Schema)
		}
		if used > nativeReadToolBytes {
			t.Fatalf("selected %d bytes, limit %d", used, nativeReadToolBytes)
		}
	}
	// Budget bytes, rather than Unicode character count.
	reg := tools.NewRegistry()
	source := nativeReadTestTool("core_source")
	source.NextTools = []string{"unicode"}
	reg.MustRegister(source)
	spec := nativeReadTestTool("unicode")
	spec.Description = strings.Repeat("ą", nativeReadToolBytes/2)
	reg.MustRegister(spec)
	if got := selectNativeReadTools(reg, func(name string) bool { return name == source.Name }); got != nil {
		t.Fatalf("oversized UTF-8 contract selected: %v", nativeReadTestNames(got))
	}
}

func TestNativeReadToolsNeverExpandRestrictedRegistry(t *testing.T) {
	if got := selectNativeReadTools(nil, nil); got != nil {
		t.Fatal("nil registry produced tools")
	}
	reg := tools.NewRegistry()
	source := nativeReadTestTool("registered_core")
	source.NextTools = []string{"forbidden_outside_registry", "permitted_reader"}
	reg.MustRegister(source)
	if got := selectNativeReadTools(reg, func(name string) bool { return name == source.Name }); got != nil {
		t.Fatalf("unknown workflow target expanded registry: %v", nativeReadTestNames(got))
	}
	reg.MustRegister(nativeReadTestTool("permitted_reader"))
	if got := nativeReadTestNames(selectNativeReadTools(reg, func(name string) bool { return name == source.Name })); !reflect.DeepEqual(got, []string{"permitted_reader"}) {
		t.Fatalf("restricted registry selected %v", got)
	}
	if got := selectNativeReadTools(reg, nil); got != nil {
		t.Fatalf("selection without any core workflow produced %v", nativeReadTestNames(got))
	}
}

func TestNativeReadToolsBoundRegisteredWorkflowToolCount(t *testing.T) {
	reg := tools.NewRegistry()
	source := nativeReadTestTool("core_source")
	for i := 11; i >= 0; i-- {
		name := fmt.Sprintf("reader_%02d", i)
		reg.MustRegister(nativeReadTestTool(name))
		source.NextTools = append(source.NextTools, name)
	}
	reg.MustRegister(source)
	got := nativeReadTestNames(selectNativeReadTools(reg, func(name string) bool { return name == source.Name }))
	want := []string{"reader_00", "reader_01", "reader_02", "reader_03", "reader_04", "reader_05", "reader_06", "reader_07"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded workflow selected %v, want %v", got, want)
	}
}
