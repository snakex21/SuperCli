package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Independent pre-cache renderer oracle, including the whole-list legacy
// fallback and RawMessage handling of numeric literals/duplicate JSON keys.
func originalRequestedContextRender(defs []llm.ToolDef) string {
	if len(defs) == 0 {
		return ""
	}
	type contract struct {
		Name, Description string
		Schema            json.RawMessage
	}
	contracts := make([]contract, len(defs))
	for i, def := range defs {
		schema := bytes.TrimSpace([]byte(def.Schema))
		if len(schema) == 0 || schema[0] != '{' || !json.Valid(schema) {
			raw, _ := json.Marshal(defs)
			return requestedToolContextPreamble + "\n" + string(raw)
		}
		contracts[i] = contract{Name: def.Name, Description: def.Description, Schema: schema}
	}
	raw, _ := json.Marshal(contracts)
	return requestedToolContextPreamble + "\n" + string(raw)
}

func originalRequestedContextForLoop(l *Loop) string {
	if !l.usesRequestedToolContext() {
		return ""
	}
	defs := l.requestedToolDefinitions()
	native := l.nativeContracts()
	filtered := defs[:0]
	for _, def := range defs {
		if !containsNativeContract(native, def.Name) {
			filtered = append(filtered, def)
		}
	}
	return originalRequestedContextRender(filtered)
}

func requestedContextCacheLoop(t *testing.T) *Loop {
	t.Helper()
	l := nativeFixture(t)
	l.nativeContracts() // Freeze initial native contracts before late workflow admission.
	l.workflowTools = []string{"apply_record", "dormant_effect"}
	l.workflowRevision++
	return l
}

func assertRequestedContextCacheEmpty(t *testing.T, snapshot *requestedToolContextSnapshot) {
	t.Helper()
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.defs != nil || snapshot.text != "" || snapshot.present.Load() {
		t.Fatal("ineligible render retained cache data")
	}
}

func TestRequestedToolContextCacheOriginalByteOracle(t *testing.T) {
	schemas := []string{
		`{ "type":"object", "examples":[9007199254740993,1e+09,-0], "x":1,"x":2, "enum":["ą日本語","two  spaces"] }`,
		"{}", "", "{", "not JSON", "null", "[]", "true", "42", `"text"`,
	}
	var snapshot requestedToolContextSnapshot
	for _, schema := range schemas {
		defs := []llm.ToolDef{
			{Name: "apply_record", Description: "Unicode ą日本語 😀 <&>\r\n", Schema: schemas[0]},
			{Name: "dormant_effect", Description: "Second exact contract", Schema: schema},
		}
		want := originalRequestedContextRender(defs)
		for i := 0; i < 3; i++ {
			if got := snapshot.render(defs); got != want {
				t.Fatalf("schema %q changed rendered bytes", schema)
			}
		}
	}
	for _, defs := range [][]llm.ToolDef{nil, {}} {
		if got := snapshot.render(defs); got != originalRequestedContextRender(defs) {
			t.Fatal("empty render changed")
		}
		assertRequestedContextCacheEmpty(t, &snapshot)
	}
	// Randomized exact byte coverage includes invalid UTF-8 and schemas that
	// use the legacy fallback. No normalization belongs in the cache key.
	rng := rand.New(rand.NewSource(72161))
	for iteration := 0; iteration < 160; iteration++ {
		defs := make([]llm.ToolDef, 1+rng.Intn(8))
		for i := range defs {
			raw := make([]byte, rng.Intn(48))
			_, _ = rng.Read(raw)
			defs[i] = llm.ToolDef{Name: fmt.Sprintf("record_%d_ą", i), Description: string(raw), Schema: schemas[rng.Intn(len(schemas))]}
		}
		want := originalRequestedContextRender(defs)
		if cold, warm := snapshot.render(defs), snapshot.render(defs); cold != want || warm != want {
			t.Fatalf("randomized renderer changed at iteration %d", iteration)
		}
	}
}

func TestRequestedToolContextCacheExactOrderAndValues(t *testing.T) {
	var snapshot requestedToolContextSnapshot
	original := []llm.ToolDef{
		{Name: "record_a", Description: "A", Schema: `{"type":"object"}`},
		{Name: "record_b", Description: "B", Schema: "{}"},
	}
	cold := snapshot.render(original)
	warm := snapshot.render(append([]llm.ToolDef(nil), original...))
	if cold != originalRequestedContextRender(original) || unsafe.StringData(cold) != unsafe.StringData(warm) {
		t.Fatal("equivalent descriptors did not reuse the completed render")
	}
	for _, change := range []struct {
		name  string
		apply func([]llm.ToolDef) []llm.ToolDef
	}{
		{"name", func(defs []llm.ToolDef) []llm.ToolDef { defs[0].Name = "record_c"; return defs }},
		{"description", func(defs []llm.ToolDef) []llm.ToolDef { defs[0].Description = "C"; return defs }},
		{"schema", func(defs []llm.ToolDef) []llm.ToolDef { defs[0].Schema = "{ }"; return defs }},
		{"order", func(defs []llm.ToolDef) []llm.ToolDef { defs[0], defs[1] = defs[1], defs[0]; return defs }},
		{"length", func(defs []llm.ToolDef) []llm.ToolDef { return defs[:1] }},
	} {
		t.Run(change.name, func(t *testing.T) {
			snapshot.render(original)
			defs := change.apply(append([]llm.ToolDef(nil), original...))
			want := originalRequestedContextRender(defs)
			if got := snapshot.render(defs); got != want || got == cold {
				t.Fatal("descriptor change reused an outdated render")
			}
			if got := snapshot.render(original); got != cold {
				t.Fatal("returning to the original descriptors changed bytes")
			}
		})
	}
}

func TestRequestedToolContextCacheBoundsAndSourceOwnership(t *testing.T) {
	base := []llm.ToolDef{{Name: "record", Description: "description", Schema: "{}"}}
	for _, kind := range []string{"count", "source bytes", "render bytes"} {
		t.Run(kind, func(t *testing.T) {
			var snapshot requestedToolContextSnapshot
			snapshot.render(base)
			defs := append([]llm.ToolDef(nil), base...)
			switch kind {
			case "count":
				defs = make([]llm.ToolDef, requestedContextCacheMaxDefs+1)
				for i := range defs {
					defs[i] = base[0]
				}
			case "source bytes":
				defs[0].Description = strings.Repeat("x", requestedContextCacheMaxSourceBytes)
			case "render bytes":
				// JSON escaping makes the render exceed its limit while the
				// actual descriptor bytes remain below the source-byte limit.
				defs[0].Description = strings.Repeat("\x00", requestedContextCacheMaxRenderBytes/6+1)
			}
			want := originalRequestedContextRender(defs)
			if got := snapshot.render(defs); got != want {
				t.Fatal("retention limit truncated or changed the emitted contract")
			}
			assertRequestedContextCacheEmpty(t, &snapshot)
			if got := snapshot.render(base); got != originalRequestedContextRender(base) {
				t.Fatal("oversized input poisoned a later bounded render")
			}
		})
	}
	var snapshot requestedToolContextSnapshot
	boundary := append([]llm.ToolDef(nil), base...)
	boundary[0].Description = strings.Repeat("x", requestedContextCacheMaxSourceBytes-len(boundary[0].Name)-len(boundary[0].Schema))
	if got := snapshot.render(boundary); got != originalRequestedContextRender(boundary) || len(snapshot.defs) != 1 {
		t.Fatal("exact source-byte boundary was not retained")
	}
	countBoundary := make([]llm.ToolDef, requestedContextCacheMaxDefs)
	for i := range countBoundary {
		countBoundary[i] = base[0]
	}
	if got := snapshot.render(countBoundary); got != originalRequestedContextRender(countBoundary) || len(snapshot.defs) != requestedContextCacheMaxDefs {
		t.Fatal("exact descriptor-count boundary was not retained")
	}
	largeSource := strings.Repeat("x", 256<<10) + `{"type":"object"}`
	defs := []llm.ToolDef{{Name: largeSource[:6], Description: largeSource[12:28], Schema: largeSource[256<<10:]}}
	want := originalRequestedContextRender(defs)
	if got := snapshot.render(defs); got != want {
		t.Fatal("owning source values changed the rendered bytes")
	}
	for _, pair := range [][2]string{
		{defs[0].Name, snapshot.defs[0].Name},
		{defs[0].Description, snapshot.defs[0].Description},
		{defs[0].Schema, snapshot.defs[0].Schema},
	} {
		if pair[0] != pair[1] || unsafe.StringData(pair[0]) == unsafe.StringData(pair[1]) {
			t.Fatal("snapshot retained a descriptor source buffer")
		}
	}
	defs[0] = base[0]
	if snapshot.defs[0].Name != largeSource[:6] || snapshot.text != want {
		t.Fatal("caller mutation changed privately retained descriptors")
	}
	snapshot.reset()
	assertRequestedContextCacheEmpty(t, &snapshot)
}

func TestRequestedToolContextCacheRefreshesAdmissionAndContracts(t *testing.T) {
	l := requestedContextCacheLoop(t)
	check := func(label string) string {
		t.Helper()
		want := originalRequestedContextForLoop(l)
		got := l.requestedToolContext()
		if got != want || l.requestedToolContext() != want {
			t.Fatalf("%s returned stale requested-tool context", label)
		}
		return got
	}
	first := check("initial late workflow")
	if !strings.Contains(first, "apply_record") || !strings.Contains(first, "dormant_effect") {
		t.Fatal("fixture did not produce a late dispatcher tail")
	}
	l.workflowTools[0], l.workflowTools[1] = l.workflowTools[1], l.workflowTools[0]
	l.workflowRevision++
	if check("reordered workflow") == first {
		t.Fatal("contract order did not refresh")
	}
	l.workflowTools = append(l.workflowTools, "late_record")
	l.workflowRevision++
	check("unregistered late workflow")
	l.registry.MustRegister(tools.Tool{Name: "late_record", Description: "Late Unicode ą日本語", Schema: `{ "type":"object", "examples":[9007199254740993,-0] }`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	if !strings.Contains(check("late registration"), "Late Unicode") {
		t.Fatal("registered late contract did not refresh")
	}
	l.registry.Activate("late_record")
	check("activation")
	l.registry.Deactivate("late_record")
	check("deactivation")
	replacement := tools.NewRegistry()
	for _, name := range []string{"apply_record", "dormant_effect", "late_record", invokeToolName} {
		tool, ok := l.registry.Get(name)
		if !ok {
			t.Fatalf("missing fixture contract %s", name)
		}
		if name == "apply_record" {
			tool.Description = "Replacement description"
			tool.Schema = `{ "type":"object", "examples":[1e+09], "x":1,"x":2 }`
		}
		replacement.MustRegister(tool)
	}
	replacement.MarkAlwaysOn(invokeToolName)
	l.SetRegistry(replacement)
	l.nativeContracts() // Freeze replacement native list before admitting workflow.
	l.workflowTools = []string{"apply_record"}
	l.workflowRevision++
	if got := check("registry replacement and changed schema"); !strings.Contains(got, "Replacement description") || strings.Contains(got, "Fixture contract apply_record") {
		t.Fatal("registry replacement retained old contract values")
	}
	// A new native snapshot may promote this same contract; output must be
	// re-filtered even though the registry and workflow values are unchanged.
	l.nativeRunTools, l.nativeRunSet = nil, false
	if got := check("native promotion"); got != "" {
		t.Fatal("native contract was still duplicated in the dispatcher tail")
	}
	assertRequestedContextCacheEmpty(t, &l.requestedContextCache)
}

func TestRequestedToolContextCacheFreshGatesAndRunScope(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*Loop)
	}{
		{"thin off", func(l *Loop) { l.thinTools = false }},
		{"stable off", func(l *Loop) { l.stableToolset = false }},
		{"chat route", func(l *Loop) { l.route = RouteChatOnly }},
		{"advisor route", func(l *Loop) { l.route = RouteAdvisor }},
		{"clarify route", func(l *Loop) { l.route = RouteClarify }},
		{"final only", func(l *Loop) { l.finalReplyOnly = true }},
		{"nil registry", func(l *Loop) { l.registry = nil }},
		{"no workflow", func(l *Loop) { l.workflowTools = nil; l.workflowRevision++ }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			l := requestedContextCacheLoop(t)
			if l.requestedToolContext() == "" {
				t.Fatal("fixture has no warm tail")
			}
			scenario.change(l)
			if got := l.requestedToolContext(); got != "" || got != originalRequestedContextForLoop(l) {
				t.Fatal("ineligible request reused a prior tail")
			}
			assertRequestedContextCacheEmpty(t, &l.requestedContextCache)
		})
	}
	t.Run("dispatcher visibility", func(t *testing.T) {
		l := requestedContextFixture(t, false)
		l.registry.Activate(invokeToolName)
		l.nativeContracts()
		l.workflowTools = []string{"headless_control"}
		l.workflowRevision++
		if l.requestedToolContext() == "" {
			t.Fatal("fixture has no warm tail")
		}
		l.registry.Deactivate(invokeToolName)
		if l.requestedToolContext() != "" {
			t.Fatal("hidden dispatcher reused a prior tail")
		}
		assertRequestedContextCacheEmpty(t, &l.requestedContextCache)
		l.registry.Activate(invokeToolName)
		if got := l.requestedToolContext(); got == "" || got != originalRequestedContextForLoop(l) {
			t.Fatal("restoring visibility did not restore the exact current contracts")
		}
	})
	t.Run("new Run", func(t *testing.T) {
		l := requestedContextCacheLoop(t)
		if l.requestedToolContext() == "" {
			t.Fatal("fixture has no warm tail")
		}
		p := &stubProvider{scripts: [][]llm.Delta{{{Content: "Done.", FinishReason: "stop"}}}}
		l.provider = p
		drainEvents(t, mustRun(t, l, "Inspect available information."))
		if len(l.workflowTools) != 0 || l.requestedToolContext() != "" {
			t.Fatal("unrelated Run inherited old workflow contracts")
		}
		for _, request := range p.reqs {
			for _, message := range request {
				if strings.Contains(message.Content, requestedToolContextPreamble) {
					t.Fatal("unrelated provider request inherited old dispatcher text")
				}
			}
		}
		assertRequestedContextCacheEmpty(t, &l.requestedContextCache)
	})
}

func TestRequestedToolContextCacheRefreshesCapabilityFlags(t *testing.T) {
	l := requestedContextFixture(t, true)
	l.nativeContracts()
	for _, flags := range []struct {
		screenshot, headless bool
		wantNames            []string
	}{
		{true, false, []string{"send_screenshot", "process_session"}},
		{false, true, []string{"headless_control", "process_session"}},
		{true, true, []string{"send_screenshot", "headless_control", "process_session"}},
		{false, false, nil},
		{true, false, []string{"send_screenshot", "process_session"}},
	} {
		l.screenshotForRun, l.headlessForRun = flags.screenshot, flags.headless
		got := l.requestedToolContext()
		if got != originalRequestedContextForLoop(l) {
			t.Fatal("changed capability flags reused the wrong render")
		}
		var names []string
		for _, def := range requestToolContracts([]llm.Message{{Role: llm.RoleSystem, Content: got}}) {
			names = append(names, def.Name)
		}
		if !reflect.DeepEqual(names, flags.wantNames) {
			t.Fatalf("changed capability flags emitted %v, want %v", names, flags.wantNames)
		}
		if len(flags.wantNames) == 0 {
			assertRequestedContextCacheEmpty(t, &l.requestedContextCache)
		}
	}
}

func TestRequestedToolContextCacheKeepsHistoryAndLiveTailFresh(t *testing.T) {
	l := requestedContextCacheLoop(t)
	native := &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: json.RawMessage(`{"reasoning_content":"fixture continuation"}`)}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: native}}, ToolCalls: []llm.ToolCall{{ID: "prior", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, ToolCallID: "prior", Name: "read_lines", Content: "fixture evidence"}, llm.Message{Role: llm.RoleUser, Content: "current Unicode ą日本語"})
	before, _ := json.Marshal(l.Messages)
	descriptors := l.buildToolDefs()
	l.keepThinking = true
	l.lastThinking, l.liveContext = "first retained reasoning", "first current requirements"
	first := l.trailingContext()
	requested := l.requestedToolContext()
	l.lastThinking, l.liveContext = "updated retained reasoning", "updated current requirements"
	second := l.trailingContext()
	if !strings.Contains(first, "first retained reasoning") || !strings.Contains(first, "first current requirements") || !strings.Contains(second, "updated retained reasoning") || !strings.Contains(second, "updated current requirements") || strings.Contains(second, "first retained reasoning") || !strings.Contains(second, requested) {
		t.Fatal("render cache froze or changed unrelated live context/thinking")
	}
	for i := 0; i < 3; i++ {
		messages, tokens := l.prepareProviderMessages(true)
		if tokens != llm.EstimateTokens(messages) {
			t.Fatal("cached tail changed exact provider-message token accounting")
		}
		if !reflect.DeepEqual(descriptors, l.buildToolDefs()) {
			t.Fatal("tail cache changed native descriptors")
		}
	}
	after, _ := json.Marshal(l.Messages)
	if !bytes.Equal(before, after) || l.Messages[len(l.Messages)-3].Parts[0].Reasoning != native {
		t.Fatal("tail cache changed canonical history or native continuation")
	}
}

func TestRequestedToolContextCacheConcurrentRenders(t *testing.T) {
	var snapshot requestedToolContextSnapshot
	var workers sync.WaitGroup
	errors := make(chan string, 12)
	for worker := 0; worker < 12; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for iteration := 0; iteration < 40; iteration++ {
				defs := []llm.ToolDef{{Name: fmt.Sprintf("record_%d", worker), Description: fmt.Sprintf("iteration_%d_ą日本語", iteration%3), Schema: `{"type":"object","examples":[9007199254740993,-0]}`}}
				if got, want := snapshot.render(defs), originalRequestedContextRender(defs); got != want {
					errors <- fmt.Sprintf("worker %d returned another render", worker)
					return
				}
				defs[0].Description = "caller changed its own slice"
				if iteration%7 == 0 {
					snapshot.reset()
				}
			}
		}(worker)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	// A reset after every publisher completed must release the last snapshot.
	snapshot.reset()
	assertRequestedContextCacheEmpty(t, &snapshot)
}
