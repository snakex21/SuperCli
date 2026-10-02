package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func requestToolContracts(messages []llm.Message) []llm.ToolDef {
	for _, message := range messages {
		if at := strings.Index(message.Content, requestedToolContextPreamble+"\n"); at >= 0 {
			var defs []llm.ToolDef
			_ = json.NewDecoder(strings.NewReader(message.Content[at+len(requestedToolContextPreamble)+1:])).Decode(&defs)
			return defs
		}
	}
	return nil
}
func requestContainsToolContract(messages []llm.Message, name string) bool {
	for _, def := range requestToolContracts(messages) {
		if def.Name == name {
			return true
		}
	}
	return false
}
func requestedContextFixture(t *testing.T, dispatcherVisible bool) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	for _, name := range []string{"send_screenshot", "headless_control", "process_session", "read_lines", "tool_search"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "Exact Unicode contract: " + name + " — ą日本語", Schema: "{\"type\":\"object\",\"properties\":{\"n\":{\"type\":\"integer\",\"minimum\":1}},\"required\":[\"n\"]}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "fixture"}, nil
		}})
	}
	reg.MarkAlwaysOn("read_lines")
	reg.MarkAlwaysOn("tool_search")
	reg.MustRegister(NewInvokeTool(reg).Spec())
	if dispatcherVisible {
		reg.MarkAlwaysOn(invokeToolName)
	}
	l, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, System: "Fixture system", ThinTools: true, StableToolset: true, CatalogHoist: true})
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func TestRequestedToolContextKeepsWirePrefixAndExactContracts(t *testing.T) {
	l := requestedContextFixture(t, true)
	l.prepareRunRoute(context.Background(), "napraw plik")
	baseline, _ := json.Marshal(l.buildToolDefs())
	for _, prompt := range []string{"zrób zrzut pulpitu", "QEMU headless screenshot", "Take screenshot of the app window"} {
		l.prepareRunRoute(context.Background(), prompt)
		got, _ := json.Marshal(l.buildToolDefs())
		if !bytes.Equal(baseline, got) {
			t.Fatalf("requested capability changed stable wire defs: %s", got)
		}
		messages, messageTokens := l.prepareProviderMessages(true)
		contracts := requestToolContracts(messages)
		if len(contracts) == 0 {
			t.Fatal("full requested contracts missing")
		}
		for _, def := range contracts {
			tool, ok := l.registry.Get(def.Name)
			if !ok || def.Description != tool.Description || def.Schema != tool.Schema {
				t.Fatalf("contract altered: %+v", def)
			}
			if l.registry.IsActive(def.Name) {
				t.Fatal("request changed activation")
			}
		}
		if len(l.registry.DiscoveredNames()) != 0 {
			t.Fatal("request changed discovery")
		}
		if actual := messageTokens + estimateRequestTokens(nil, l.buildToolDefs()); actual != estimateRequestTokens(messages, l.buildToolDefs()) {
			t.Fatalf("prepared estimator=%d exact wire=%d", actual, estimateRequestTokens(messages, l.buildToolDefs()))
		}
		withTail := l.nextRequestTokenEstimate().Raw
		tailTokens := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.contextTail()})
		screenshot, headless := l.screenshotForRun, l.headlessForRun
		l.screenshotForRun, l.headlessForRun = false, false
		withoutTail := l.nextRequestTokenEstimate().Raw
		plainTailTokens := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.contextTail()})
		l.screenshotForRun, l.headlessForRun = screenshot, headless
		if withTail-withoutTail != tailTokens-plainTailTokens {
			t.Fatal("request estimator did not price requested contracts exactly once")
		}
	}
	l.prepareRunRoute(context.Background(), "napraw plik")
	if l.requestedToolContext() != "" {
		t.Fatal("unrelated run retained ephemeral contracts")
	}
	after, _ := json.Marshal(l.buildToolDefs())
	if !bytes.Equal(baseline, after) {
		t.Fatal("removing requested contracts changed core prefix")
	}
}
func TestRequestedToolContextFallsBackWithoutAdvertisedDispatcher(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Loop)
	}{
		{"thin off", func(l *Loop) { l.thinTools = false }},
		{"stable off", func(l *Loop) { l.stableToolset = false }},
		{"hidden dispatcher", func(l *Loop) { l.registry.ResetVisibility() }},
		{"orchestrator", func(l *Loop) { l.orchestrator = true }},
		{"absent dispatcher", func(l *Loop) {
			reg := tools.NewRegistry()
			tool, _ := l.registry.Get("send_screenshot")
			reg.MustRegister(tool)
			l.SetRegistry(reg)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := requestedContextFixture(t, tc.name != "hidden dispatcher")
			l.prepareRunRoute(context.Background(), "Take screenshot")
			tc.change(l)
			if l.requestedToolContext() != "" {
				t.Fatal("tail supplied an unavailable dispatcher")
			}
			if !hasScreenshotSchema(l.buildToolDefs()) {
				t.Fatal("native fallback lost requested capability")
			}
		})
	}
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		l := requestedContextFixture(t, true)
		l.screenshotForRun = true
		l.route = route
		if l.requestedToolContext() != "" {
			t.Fatal("light route acquired coordinator contracts")
		}
	}
	l := requestedContextFixture(t, true)
	l.prepareRunRoute(context.Background(), "Take screenshot")
	l.finalReplyOnly = true
	if l.requestedToolContext() != "" || l.buildToolDefs() != nil || strings.Contains(l.trailingContext(), requestedToolContextPreamble) {
		t.Fatal("tool-free final guard gained tools")
	}
	l.SetRegistry(tools.NewRegistry())
	l.finalReplyOnly = false
	if l.requestedToolContext() != "" {
		t.Fatal("restricted registry gained a tool")
	}
}
func TestRequestedToolContextRefreshesRegistryAndPreservesHistory(t *testing.T) {
	l := requestedContextFixture(t, true)
	l.prepareRunRoute(context.Background(), "Take screenshot")
	before := l.requestedToolContext()
	next := tools.NewRegistry()
	source, _ := l.registry.Get("send_screenshot")
	source.Description += " replacement"
	source.Schema = "{ \"type\": \"object\", \"properties\": {\"n\":{\"type\":\"integer\",\"minimum\":2}}, \"required\":[\"n\"] }"
	next.MustRegister(source)
	next.MustRegister(NewInvokeTool(next).Spec())
	next.MarkAlwaysOn(invokeToolName)
	l.SetRegistry(next)
	if got := l.requestedToolContext(); got == before || !strings.Contains(got, "replacement") {
		t.Fatal("registry replacement kept stale contract")
	}
	late := tools.Tool{Name: "process_session", Description: "late process", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }}
	next.MustRegister(late)
	if !strings.Contains(l.requestedToolContext(), "late process") {
		t.Fatal("late registration omitted")
	}
	native := &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: json.RawMessage("{\"reasoning_content\":\"fixture continuation\"}")}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: native}}, ToolCalls: []llm.ToolCall{{ID: "prior", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, ToolCallID: "prior", Name: "read_lines", Content: "fixture"}, llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Unicode ą日本語"}, {Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "dormant", Name: "fixture.png"}}}})
	original, _ := json.Marshal(l.Messages)
	for i := 0; i < 2; i++ {
		l.prepareProviderMessages(true)
	}
	after, _ := json.Marshal(l.Messages)
	if !bytes.Equal(original, after) || l.Messages[len(l.Messages)-3].Parts[0].Reasoning != native {
		t.Fatal("request tail rewrote raw parts/native history")
	}
	for _, msg := range l.Messages {
		if strings.Contains(msg.Content, requestedToolContextPreamble) {
			t.Fatal("tail persisted into transcript")
		}
	}
}

type requestedOwnedWorkflowProvider struct {
	t        *testing.T
	calls    int
	defsJSON []byte
	system   string
	desktop  bool
}

func (*requestedOwnedWorkflowProvider) Name() string { return "owned-workflow-fixture" }
func (p *requestedOwnedWorkflowProvider) Complete(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	raw, _ := json.Marshal(defs)
	if p.calls == 1 {
		p.defsJSON = raw
		p.system = messages[0].Content
	} else if !bytes.Equal(p.defsJSON, raw) || p.system != messages[0].Content {
		p.t.Fatal("workflow changed stable prefix")
	}
	if !requestContainsToolContract(messages, "send_screenshot") || !requestContainsToolContract(messages, "process_session") {
		p.t.Fatal("workflow omitted complete requested capability")
	}
	for _, def := range defs {
		if def.Name == "send_screenshot" || def.Name == "process_session" {
			p.t.Fatal("requested schema moved into stable core")
		}
	}
	var call llm.ToolCall
	var final bool
	if p.desktop {
		final = p.calls > 1
		call = llm.ToolCall{ID: "desktop-shot", Name: "invoke_tool", Arguments: "{\"tool\":\"send_screenshot\",\"args\":{\"source\":\"desktop\",\"attach\":false}}"}
	} else {
		switch p.calls {
		case 1:
			call = llm.ToolCall{ID: "owned-start", Name: "invoke_tool", Arguments: "{\"tool\":\"process_session\",\"args\":{\"action\":\"start\",\"command\":[\"fixture-app\"]}}"}
		case 2:
			call = llm.ToolCall{ID: "owned-shot", Name: "invoke_tool", Arguments: "{\"tool\":\"process_session\",\"args\":{\"action\":\"screenshot\",\"id\":\"fixture-owned\"}}"}
		default:
			final = true
		}
	}
	ch := make(chan llm.Delta, 1)
	if final {
		ch <- llm.Delta{Content: "Fixture screenshot ready.", FinishReason: "stop"}
	} else {
		ch <- llm.Delta{ToolCall: &call, FinishReason: "tool_calls"}
	}
	close(ch)
	return ch, nil
}
func TestRequestedToolContextPublicRunDesktopAndOwnedWorkflow(t *testing.T) {
	for _, desktop := range []bool{true, false} {
		t.Run(map[bool]string{true: "desktop", false: "owned-window"}[desktop], func(t *testing.T) {
			l := requestedContextFixture(t, true)
			reg := tools.NewRegistry()
			executed := []string{}
			capture := tools.NewSendScreenshot(t.TempDir(), nil).Spec()
			capture.Fn = func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				var args map[string]any
				_ = json.Unmarshal(raw, &args)
				if args["source"] != "desktop" || args["attach"] != false {
					t.Fatal("desktop dispatch arguments altered")
				}
				executed = append(executed, "desktop")
				return tools.Result{Text: "fixture desktop saved"}, nil
			}
			reg.MustRegister(capture)
			process := tools.NewProcessSession(t.TempDir())
			defer process.Close()
			spec := process.Spec()
			spec.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				if ctx.Err() != nil {
					return tools.Result{}, ctx.Err()
				}
				var args map[string]any
				_ = json.Unmarshal(raw, &args)
				executed = append(executed, args["action"].(string))
				return tools.Result{Text: "{\"id\":\"fixture-owned\",\"running\":true}"}, nil
			}
			reg.MustRegister(spec)
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn(invokeToolName)
			l.SetRegistry(reg)
			p := &requestedOwnedWorkflowProvider{t: t, desktop: desktop}
			l.provider = p
			prompt := "Take screenshot of desktop"
			want := []string{"desktop"}
			if !desktop {
				prompt = "Launch the app and take a screenshot of its window"
				want = []string{"start", "screenshot"}
			}
			drainEvents(t, mustRun(t, l, prompt))
			if !reflect.DeepEqual(executed, want) || p.calls != len(want)+1 || l.InvokeToolDispatches() != int64(len(want)) {
				t.Fatalf("executed=%v provider=%d dispatch=%d", executed, p.calls, l.InvokeToolDispatches())
			}
			for _, msg := range l.Messages {
				if strings.Contains(msg.Content, requestedToolContextPreamble) {
					t.Fatal("provider contract became transcript content")
				}
				for _, call := range msg.ToolCalls {
					if call.Name == invokeToolName {
						t.Fatal("history lost canonical target attribution")
					}
				}
			}
			l.prepareRunRoute(context.Background(), "napraw plik main.go")
			if l.requestedToolContext() != "" {
				t.Fatal("next run retained workflow contract")
			}
		})
	}
}
func TestRequestedToolContextKeepsTargetValidation(t *testing.T) {
	l := requestedContextFixture(t, true)
	l.prepareRunRoute(context.Background(), "Take screenshot")
	calls := l.resolveInvokeToolCalls([]llm.ToolCall{{ID: "invalid", Name: invokeToolName, Arguments: "{\"tool\":\"send_screenshot\",\"args\":{\"n\":0}}"}})
	if calls[0].Name != "send_screenshot" {
		t.Fatal("requested target unavailable")
	}
	result, err := l.registry.Execute(context.Background(), calls[0].Name, json.RawMessage(calls[0].Arguments))
	if err == nil && result.Err == nil {
		t.Fatal("dispatcher bypassed target minimum validation")
	}
	l.prepareRunRoute(context.Background(), "napraw plik")
	resolved := l.resolveInvokeToolCalls([]llm.ToolCall{{Name: invokeToolName, Arguments: "{\"tool\":\"process_session\",\"args\":{\"n\":1}}"}})
	if resolved[0].Name != invokeToolName {
		t.Fatal("run allowance leaked")
	}
}
