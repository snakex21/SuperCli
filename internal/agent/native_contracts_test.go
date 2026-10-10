package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func nativeFixture(t *testing.T) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "web_lookup", Description: "Core fixture discovery", ReadOnly: true,
		NextTools: []string{"inspect_record", "inspect_details"}, Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Fixture discovery"}, nil
		},
	})
	reg.MarkAlwaysOn("web_lookup")
	for _, name := range []string{"inspect_record", "inspect_details", "apply_record", "dormant_effect"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "Fixture contract " + name, ReadOnly: strings.HasPrefix(name, "inspect_"),
			Schema: `{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Text: "Fixture result"}, nil
			},
		})
	}
	reg.MustRegister(NewInvokeTool(reg).Spec())
	reg.MarkAlwaysOn(invokeToolName)
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{}, Registry: reg, ThinTools: true, StableToolset: true, CatalogHoist: true})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNativeContractsKeepWireStableThroughDiscovery(t *testing.T) {
	l := nativeFixture(t)
	l.workflowTools = []string{"apply_record"} // this contract is already admitted for the instruction
	first := l.buildToolDefs()
	if !containsNativeContract(first, "inspect_record") || !containsNativeContract(first, "apply_record") || containsNativeContract(first, "dormant_effect") {
		t.Fatal("ready contracts missing or unrelated effect advertised")
	}
	if tail := l.requestedToolContext(); strings.Contains(tail, "apply_record") || strings.Contains(tail, "inspect_record") {
		t.Fatal("native contract also advertised as invoke-only")
	}
	l.registry.ActivateDiscovered("inspect_record", "dormant_effect")
	l.workflowTools = append(l.workflowTools, "dormant_effect")
	l.workflowRevision++
	second := l.buildToolDefs()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("activation rewrote the stable native schema prefix")
	}
	if !strings.Contains(l.requestedToolContext(), "dormant_effect") {
		t.Fatal("later admitted effect lost its dispatcher contract")
	}
	l.nativeRunTools, l.nativeRunSet = nil, false
	l.workflowTools = nil
	l.workflowRevision++
	third := l.buildToolDefs()
	if containsNativeContract(third, "apply_record") || !containsNativeContract(third, "inspect_record") {
		t.Fatal("next instruction inherited an old requested effect or lost stable reads")
	}
}

func TestNativeContractsRoutesRegistryAndBudget(t *testing.T) {
	l := nativeFixture(t)
	l.route = RouteAdvisor
	if l.nativeContracts() != nil || l.nativeRunSet || l.nativeReadsSet {
		t.Fatal("light route froze coordinator contracts early")
	}
	l.route = RouteCoordinator
	if !containsNativeContract(l.buildToolDefs(), "inspect_record") {
		t.Fatal("escalation lost reads")
	}
	l.SetRegistry(tools.NewRegistry())
	if len(l.nativeReads) != 0 || len(l.nativeRunTools) != 0 || l.nativeReadsSet || l.nativeRunSet {
		t.Fatal("registry reset retained capabilities")
	}
	if len(l.nativeContracts()) != 0 {
		t.Fatal("restricted registry recovered old capabilities")
	}
	for _, profile := range []struct{ thin, stable bool }{{false, true}, {true, false}} {
		l = nativeFixture(t)
		l.thinTools, l.stableToolset = profile.thin, profile.stable
		if l.nativeContracts() != nil {
			t.Fatal("legacy profile changed")
		}
	}
	l = nativeFixture(t)
	l.finalReplyOnly = true
	if len(l.buildToolDefs()) != 0 || l.nativeRunSet {
		t.Fatal("final-only route advertised tools")
	}
	l = nativeFixture(t)
	l.nativeReads = []llm.ToolDef{{Name: "oversized", Description: strings.Repeat("x", nativeContractBytes+1)}}
	l.nativeReadsSet = true
	if len(l.nativeContracts()) != 0 {
		t.Fatal("native contract cap not enforced")
	}
}

func TestNativeContractsRunExpiresInitialRequestSnapshot(t *testing.T) {
	l := nativeFixture(t)
	expectedCount := len(l.buildToolDefs())
	l.nativeRunTools, l.nativeRunSet = nil, false
	l.workflowRevision++
	l.workflowTools = []string{"apply_record"}
	l.nativeContracts() // pre-Run context estimator must not authorize the next instruction
	p := &stubProvider{scripts: [][]llm.Delta{{{Content: "Done.", FinishReason: "stop"}}}}
	l.provider = p
	drainEvents(t, mustRun(t, l, "Inspect available information."))
	if l.nativeRunSet || len(l.nativeRunTools) != 0 {
		t.Fatal("Run retained requested native contracts")
	}
	if p.toolReqs[0] != expectedCount || containsNativeContract(p.toolDefsReqs[0], "apply_record") {
		t.Fatal("pre-Run estimator leaked an old requested effect")
	}
}

func TestNativeContractsContextReportCountsActualSchemas(t *testing.T) {
	l := nativeFixture(t)
	l.workflowTools = []string{"apply_record"}
	defs := l.buildToolDefs()
	var schemaTokens int
	for _, def := range defs {
		schemaTokens += (len(def.Name) + len(def.Description) + len(def.Schema)) / 4
	}
	report := l.ContextReport()
	if report.ToolCount != len(defs) || report.ToolSchemaTokens != schemaTokens {
		t.Fatalf("native schemas omitted from report: got count/tokens=%d/%d want=%d/%d", report.ToolCount, report.ToolSchemaTokens, len(defs), schemaTokens)
	}
	if report.RawRequestTokens != l.nextRequestTokenEstimate().Raw {
		t.Fatal("schema inventory changed request token accounting")
	}
}
