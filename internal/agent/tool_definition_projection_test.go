package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

func TestRequestedToolDefinitionsRawSchemaAndLegacyFallback(t *testing.T) {
	schema := " { \"type\": \"object\", \"properties\": {\"mode\":{\"type\":\"string\",\"enum\":[\"two  spaces\",\"ą日本語\"]}}, \"examples\":[9007199254740993,1e+09,-0], \"x\":1,\"x\":2 } "
	defs := []llm.ToolDef{{Name: "send_screenshot", Description: "ą日本語", Schema: schema}}
	raw := renderRequestedToolDefinitions(defs)
	var got []struct {
		Name, Description string
		Schema            json.RawMessage
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(schema)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != defs[0].Name || got[0].Description != defs[0].Description || !bytes.Equal(got[0].Schema, compact.Bytes()) {
		t.Fatalf("contract lost literal/schema facts: %s", raw)
	}
	if defs[0].Schema != schema {
		t.Fatal("render mutated registered schema")
	}
	legacy, _ := json.Marshal(defs)
	if len(raw) >= len(legacy) {
		t.Fatal("object rendering did not remove escaped-schema overhead")
	}
	for _, invalid := range []string{"", "not JSON", "{", "null", "[]", "true", "42", "\"text\""} {
		mixed := append(append([]llm.ToolDef(nil), defs...), llm.ToolDef{Name: "process_session", Description: "legacy", Schema: invalid})
		want, _ := json.Marshal(mixed)
		if got := renderRequestedToolDefinitions(mixed); got != string(want) {
			t.Fatalf("legacy schema %q changed representation", invalid)
		}
	}
}

func projectionFixture(t *testing.T, custom bool) (*Loop, string) {
	t.Helper()
	reg := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "Read", ReadOnly: true, Schema: "{\"file\":{\"type\":\"string\"}}", Fn: noop})
	// Keep one eligible read behind the dispatcher to exercise catalog
	// preservation: native read selection deliberately has a bounded budget.
	reg.MustRegister(tools.Tool{Name: "read_context", Description: strings.Repeat("c", nativeReadToolBytes+1), ReadOnly: true, Schema: "{\"query\":{\"type\":\"string\"}}", Fn: noop})
	reg.MarkAlwaysOn("read_lines")
	invoke := NewInvokeTool(reg).Spec()
	if custom {
		invoke.Description += " Custom instructions: keep all eligible facts."
	}
	reg.MustRegister(invoke)
	reg.MarkAlwaysOn(invokeToolName)
	l, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, System: "Fixture", ThinTools: true, StableToolset: true, CatalogHoist: true})
	if err != nil {
		t.Fatal(err)
	}
	l.route = RouteCoordinator
	return l, invoke.Description
}
func projectedInvoke(defs []llm.ToolDef) string {
	for _, def := range defs {
		if def.Name == invokeToolName {
			return def.Description
		}
	}
	return ""
}
func TestInvokeDescriptionProjectionPreservesDormantAndFormalSpec(t *testing.T) {
	l, registered := projectionFixture(t, false)
	before, _ := l.registry.Get(invokeToolName)
	got := projectedInvoke(l.buildToolDefs())
	if strings.Contains(got, "read_lines(") || !strings.Contains(got, "read_context(query:string)") {
		t.Fatalf("wrong redundant/dormant signatures: %s", got)
	}
	after, _ := l.registry.Get(invokeToolName)
	if after.Description != registered || before.Schema != after.Schema {
		t.Fatal("wire projection mutated formal Spec")
	}
	baseline, _ := json.Marshal(l.buildToolDefs())
	for _, prompt := range []string{"Take screenshot of the desktop", "QEMU headless", "fix project"} {
		l.prepareRunRoute(context.Background(), prompt)
		current, _ := json.Marshal(l.buildToolDefs())
		if !bytes.Equal(baseline, current) {
			t.Fatal("requested flags changed stable native prefix")
		}
	}
	defs := l.buildToolDefs()
	for i := range defs {
		if defs[i].Name == invokeToolName {
			defs[i].Description = "provider mutation"
		}
	}
	if projectedInvoke(l.buildToolDefs()) != got {
		t.Fatal("provider alias escaped snapshot")
	}
	replacement, _ := projectionFixture(t, false)
	l.SetRegistry(replacement.registry)
	if projectedInvoke(l.buildToolDefs()) != got {
		t.Fatal("registry replacement retained stale projection")
	}
	// An unrelated late eligible tool does not rewrite a frozen registered catalog
	// or change the stable wire prefix.
	replacement.registry.MustRegister(tools.Tool{Name: "late_read", Description: "Late", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	if projectedInvoke(l.buildToolDefs()) != got {
		t.Fatal("unrelated late tool changed stable projection")
	}
}
func TestInvokeDescriptionProjectionProfilesAndCustomFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Loop)
	}{
		{"full", func(l *Loop) { l.thinTools = false }},
		{"dynamic", func(l *Loop) { l.stableToolset = false }},
		{"orchestrator", func(l *Loop) { l.orchestrator = true }},
		{"chat", func(l *Loop) { l.route = RouteChatOnly }},
		{"advisor", func(l *Loop) { l.route = RouteAdvisor }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, original := projectionFixture(t, false)
			tc.change(l)
			defs := []llm.ToolDef{{Name: "read_lines", Schema: "{}"}, {Name: invokeToolName, Description: original, Schema: NewInvokeTool(l.registry).Spec().Schema}}
			before := append([]llm.ToolDef(nil), defs...)
			l.projectInvokeToolDescription(defs)
			if !reflect.DeepEqual(before, defs) {
				t.Fatal("fallback profile changed")
			}
		})
	}
	l, original := projectionFixture(t, true)
	if projectedInvoke(l.buildToolDefs()) != original {
		t.Fatal("custom dispatcher description rewritten")
	}
	l, original = projectionFixture(t, false)
	defs := l.buildToolDefsUncached()
	for i := range defs {
		if defs[i].Name == invokeToolName {
			defs[i].Description = original
			defs[i].Schema = "{}"
		}
	}
	l.projectInvokeToolDescription(defs)
	if projectedInvoke(defs) != original {
		t.Fatal("custom dispatcher schema rewritten")
	}
	// With no native full-schema read, its eligible signature must be retained.
	defs = []llm.ToolDef{{Name: invokeToolName, Description: original, Schema: NewInvokeTool(l.registry).Spec().Schema}}
	l.projectInvokeToolDescription(defs)
	if projectedInvoke(defs) != original {
		t.Fatal("non-wire signature lost")
	}
	l.finalReplyOnly = true
	if l.buildToolDefs() != nil {
		t.Fatal("final reply guard gained projected definitions")
	}
}
