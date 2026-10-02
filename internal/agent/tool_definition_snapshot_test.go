package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"unsafe"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func definitionSnapshotFixture() *Loop {
	reg := tools.NewRegistry()
	for _, name := range []string{"tool_search", "web_lookup", "recall", "read_lines", "task", "extension_tool", "read_docx", "edit_docx", sessionImageToolName, "send_screenshot", "headless_control", "process_session"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "Exact schema for " + name, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
		reg.MarkAlwaysOn(name)
	}
	return &Loop{registry: reg, route: RouteCoordinator, thinTools: true, stableToolset: true}
}

func assertSnapshotCost(t *testing.T, l *Loop, label string) {
	t.Helper()
	want := l.buildToolDefsUncached()
	for _, phase := range []string{"cold", "warm"} {
		got := l.buildToolDefs()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s/%s: got=%v want=%v", label, phase, got, want)
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%s: wire changed", label)
		}
		if cost := l.toolDefinitionTokens(); cost != estimateRequestTokens(nil, want) {
			t.Fatalf("%s: cost=%d want=%d", label, cost, estimateRequestTokens(nil, want))
		}
		if got != nil && cap(got) != len(got) {
			t.Fatalf("%s: provider slice has spare capacity", label)
		}
	}
}

func TestToolDefinitionSnapshotInvalidation(t *testing.T) {
	l := definitionSnapshotFixture()
	for _, scenario := range []struct {
		name  string
		apply func()
	}{
		{"initial", func() {}},
		{"activate word", func() { l.registry.Activate("read_docx", "edit_docx") }},
		{"deactivate word", func() { l.registry.Deactivate("read_docx", "edit_docx") }},
		{"dynamic thin", func() { l.stableToolset = false }},
		{"discover extension", func() { l.registry.ActivateDiscovered("extension_tool") }},
		{"reset discovery", func() { l.registry.ResetVisibility() }},
		{"late register", func() {
			l.registry.MustRegister(tools.Tool{Name: "late", Description: "Later contract", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
			l.registry.Activate("late")
		}},
		{"non thin", func() { l.thinTools = false }},
		{"thin restored", func() { l.thinTools = true }},
		{"orchestrator", func() { l.orchestrator = true }},
		{"orchestrator restored", func() { l.orchestrator = false }},
		{"stable restored", func() { l.stableToolset = true }},
		{"screenshot", func() { l.screenshotForRun = true }},
		{"screenshot off", func() { l.screenshotForRun = false }},
		{"headless", func() { l.headlessForRun = true }},
		{"headless off", func() { l.headlessForRun = false }},
		{"chat", func() { l.route = RouteChatOnly }},
		{"advisor", func() { l.route = RouteAdvisor }},
		{"clarify", func() { l.route = RouteClarify }},
		{"image", func() {
			l.Messages = []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "snapshot", Active: true}}}}}
		}},
		{"dormant image", func() { l.Messages[0].Parts[0].Image.Active = false }},
		{"remove images", func() { l.Messages = nil }},
		{"coordinator", func() { l.route = RouteCoordinator }},
		{"final only", func() { l.finalReplyOnly = true }},
		{"final restored", func() { l.finalReplyOnly = false }},
	} {
		t.Run(scenario.name, func(t *testing.T) { scenario.apply(); assertSnapshotCost(t, l, scenario.name) })
	}
	replacement := tools.NewRegistry()
	l.SetRegistry(replacement)
	if l.toolDefsSnapshot.valid || l.toolDefsSnapshot.defs != nil || l.toolDefsSnapshot.key.registry != nil {
		t.Fatal("SetRegistry retained old definitions")
	}
	assertSnapshotCost(t, l, "replaced registry")
}

func TestToolDefinitionSnapshotProviderOwnership(t *testing.T) {
	l := definitionSnapshotFixture()
	request := l.buildToolDefs()
	want := append([]llm.ToolDef(nil), request...)
	request[0] = llm.ToolDef{Name: "mutated", Description: "Changed", Schema: "[]"}
	request = append(request, llm.ToolDef{Name: "provider-gate"})
	if got := l.buildToolDefs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("provider mutated cached definitions: %v", got)
	}
	l.registry.Activate("read_docx")
	l.buildToolDefs()
	if request[0].Name != "mutated" || request[len(request)-1].Name != "provider-gate" {
		t.Fatal("new snapshot mutated old provider ownership")
	}
	got, _ := l.registry.Get(want[0].Name)
	if got.Description != want[0].Description || got.Schema != want[0].Schema {
		t.Fatal("provider mutated registry")
	}
}

func TestToolDefinitionSnapshotRejectsChangedRevision(t *testing.T) {
	l := definitionSnapshotFixture()
	before := l.currentToolDefinitionKey()
	defs := l.buildToolDefsUncached()
	l.registry.ActivateDiscovered("read_docx")
	if l.toolDefsSnapshot.storeIfCurrent(before, defs, estimateRequestTokens(nil, defs)) || l.toolDefsSnapshot.valid {
		t.Fatal("published stale registry revision")
	}
	assertSnapshotCost(t, l, "revision changed during preparation")
}

func TestToolDefinitionSnapshotConcurrentRegistryUpdates(t *testing.T) {
	l := definitionSnapshotFixture()
	var done sync.WaitGroup
	done.Add(3)
	go func() {
		defer done.Done()
		for i := 0; i < 100; i++ {
			l.registry.Activate("read_docx")
			l.registry.Deactivate("read_docx")
		}
	}()
	for reader := 0; reader < 2; reader++ {
		go func() {
			defer done.Done()
			for i := 0; i < 100; i++ {
				defs := l.buildToolDefs()
				if len(defs) != 0 {
					defs[0].Name = "reader-owned"
				}
				l.toolDefinitionTokens()
			}
		}()
	}
	done.Wait()
	l.registry.Activate("read_docx")
	assertSnapshotCost(t, l, "stable after concurrent updates")
}

func TestToolDefinitionSnapshotNoOpKeepsSnapshot(t *testing.T) {
	l := definitionSnapshotFixture()
	l.buildToolDefs()
	before := l.toolDefsSnapshot.defs
	l.registry.MarkAlwaysOn("read_lines")
	l.registry.Activate("absent")
	l.registry.Deactivate("absent")
	l.buildToolDefs()
	if len(before) == 0 || &l.toolDefsSnapshot.defs[0] != &before[0] {
		t.Fatal("no-op replaced snapshot")
	}
}

// One cold or warm definition build plus the exact final-request cost. The
// miss variant switches a real request flag, rather than manually clearing cache.
func BenchmarkToolDefinitionSnapshot(b *testing.B) {
	for _, thin := range []bool{false, true} {
		for _, cold := range []bool{false, true} {
			b.Run(fmt.Sprintf("thin=%v/cold=%v", thin, cold), func(b *testing.B) {
				l := contextPreparationFixture(b, thin)
				l.buildToolDefs()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if cold {
						l.screenshotForRun = !l.screenshotForRun
					}
					defs := l.buildToolDefs()
					preparedRequestEstimateSink = estimateRequestTokens(nil, defs)
				}
				b.StopTimer()
				b.ReportMetric(float64(len(l.buildToolDefs())), "definitions")
				b.ReportMetric(float64(cap(l.toolDefsSnapshot.defs))*float64(unsafe.Sizeof(llm.ToolDef{})), "resident-def-bytes")
				b.ReportMetric(float64(unsafe.Sizeof(toolDefinitionSnapshot{})), "resident-state-bytes")
			})
		}
	}
}

// Even the miss path is prepared through the real prune/compact/wire sequence.
func BenchmarkToolDefinitionSnapshotSequenceMiss(b *testing.B) {
	for _, thin := range []bool{false, true} {
		b.Run(fmt.Sprintf("thin=%v", thin), func(b *testing.B) {
			l := contextPreparationFixture(b, thin)
			l.EstimateNextRequestTokens()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.screenshotForRun = !l.screenshotForRun
				l.EstimateNextRequestTokens()
				l.EstimateNextRequestTokens()
				defs := l.buildToolDefs()
				messages, tokens := l.prepareProviderMessages(true)
				preparedRequestEstimateSink = tokens + estimateRequestTokens(nil, defs)
				preparedRequestMessagesSink = messages
			}
		})
	}
}
