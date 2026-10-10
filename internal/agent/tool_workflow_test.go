package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func workflowFixture(t *testing.T, dispatcher bool) *Loop {
	t.Helper()
	l := requestedContextFixture(t, dispatcher)
	l.registry.MustRegister(tools.Tool{Name: "web_lookup", Description: "Search this fixture", ReadOnly: true, Schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`,
		NextTools: []string{"inspect_asset", "save_asset", "missing_tool"}, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Found https://example.com/asset.bin"}, nil
		}})
	l.registry.MarkAlwaysOn("web_lookup")
	l.registry.MustRegister(tools.Tool{Name: "inspect_asset", Description: "Inspect selected asset metadata", ReadOnly: true, Schema: `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`,
		NextTools: []string{"save_asset"}, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Asset metadata"}, nil
		}})
	l.registry.MustRegister(tools.Tool{Name: "save_asset", Description: "Save selected asset", Schema: `{"type":"object","properties":{"url":{"type":"string"},"path":{"type":"string"},"max_bytes":{"type":"integer","maximum":100}},"required":["url","path"],"additionalProperties":false}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Saved asset"}, nil
		}})
	return l
}

func TestWorkflowContractsFollowSuccessfulToolsAndExpire(t *testing.T) {
	l := workflowFixture(t, true)
	l.prepareRunRoute(context.Background(), "choose something useful")
	before, _ := json.Marshal(l.buildToolDefs())
	initial, _ := l.prepareProviderMessages(true)
	if !containsNativeContract(l.buildToolDefs(), "inspect_asset") || containsNativeContract(l.buildToolDefs(), "save_asset") {
		t.Fatal("simple inspection missing or dormant effect exposed before execution")
	}
	if l.requestedToolContext() != "" {
		t.Fatal("workflow advertised before execution")
	}
	calls := []llm.ToolCall{{Name: "web_lookup"}}
	l.learnWorkflowTools(calls, []callOutcome{{failed: true}})
	if len(l.workflowTools) != 0 {
		t.Fatal("failed search enabled contracts")
	}
	l.learnWorkflowTools(calls, []callOutcome{{inert: true}})
	if len(l.workflowTools) != 0 {
		t.Fatal("inert search enabled contracts")
	}
	l.learnWorkflowTools(calls, []callOutcome{{}})
	revision := l.workflowRevision
	l.learnWorkflowTools(calls, []callOutcome{{}})
	if l.workflowRevision != revision || len(l.workflowTools) != 2 {
		t.Fatal("duplicate or unregistered contract")
	}
	after, _ := json.Marshal(l.buildToolDefs())
	if !bytes.Equal(before, after) {
		t.Fatal("stable schema set changed")
	}
	messages, tokens := l.prepareProviderMessages(true)
	if messages[0].Content != initial[0].Content {
		t.Fatal("stable prefix changed")
	}
	contracts := requestToolContracts(messages)
	if len(contracts) != 1 || contracts[0].Name != "save_asset" || !requestContainsToolContract(messages, "inspect_asset", l.buildToolDefs()) {
		t.Fatalf("contracts=%v", contracts)
	}
	if tokens+estimateRequestTokens(nil, l.buildToolDefs()) != estimateRequestTokens(messages, l.buildToolDefs()) {
		t.Fatal("contract budget mismatch")
	}
	call := []llm.ToolCall{{Name: invokeToolName, Arguments: `{"tool":"save_asset","args":{"url":"https://example.com/a","path":"a.bin","max_bytes":101}}`}}
	resolved := l.resolveInvokeToolCalls(call)
	if resolved[0].Name != "save_asset" {
		t.Fatal("workflow dispatch unavailable")
	}
	res, err := l.registry.Execute(context.Background(), resolved[0].Name, json.RawMessage(resolved[0].Arguments))
	if err == nil && res.Err == nil {
		t.Fatal("target maximum validation bypassed")
	}
	if len(l.registry.DiscoveredNames()) != 0 || l.registry.IsActive("save_asset") {
		t.Fatal("workflow became persistent discovery")
	}
	l.provider = echoProvider("ready")
	drainEvents(t, mustRun(t, l, "cześć"))
	if len(l.workflowTools) != 0 || l.requestedToolContext() != "" {
		t.Fatal("workflow leaked into next run")
	}
}

func TestWorkflowNativeFallbackAndRegistryReplacement(t *testing.T) {
	l := workflowFixture(t, false)
	l.prepareRunRoute(context.Background(), "choose something")
	initial := l.buildToolDefs()
	l.learnWorkflowTools([]llm.ToolCall{{Name: "web_lookup"}}, []callOutcome{{}})
	defs := l.buildToolDefs()
	if len(defs) != len(initial)+1 || !containsNativeContract(initial, "inspect_asset") || !containsNativeContract(defs, "save_asset") {
		t.Fatalf("native definitions before=%d after=%d", len(initial), len(defs))
	}
	if l.requestedToolContext() != "" {
		t.Fatal("dispatcherless loop got envelope context")
	}
	l.finalReplyOnly = true
	if l.buildToolDefs() != nil {
		t.Fatal("final-only mode gained tools")
	}
	l.finalReplyOnly = false
	l.SetRegistry(tools.NewRegistry())
	if len(l.workflowTools) != 0 || len(l.requestedToolDefinitions()) != 0 {
		t.Fatal("restricted registry retained contracts")
	}
}

func TestWorkflowCapabilityDoesNotAuthorizeDownloads(t *testing.T) {
	l := workflowFixture(t, true)
	l.registry.MustRegister(tools.NewWebDownload(t.TempDir()).Spec())
	l.registry.MustRegister(tools.Tool{Name: "asset_source", Description: "Selected assets", NextTools: []string{"web_download"}, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	l.learnWorkflowTools([]llm.ToolCall{{Name: "asset_source"}}, []callOutcome{{}})
	if !requestContainsToolContract(l.providerMessages(), "web_download", l.buildToolDefs()) {
		t.Fatal("metadata did not provide the download capability")
	}
	// Metadata availability gives no filesystem grant. A normal registered
	// downloader still rejects an external path before making an HTTP request.
	args, _ := json.Marshal(map[string]string{"url": "https://example.com/a.gif", "path": t.TempDir() + "/a.gif"})
	res, err := l.registry.Execute(context.Background(), "web_download", args)
	if err == nil && res.Err == nil {
		t.Fatal("tool metadata created export authorization")
	}
}

func TestWorkflowRunCompletesWithoutDiscoveryOrSearchLoop(t *testing.T) {
	l := workflowFixture(t, true)
	p := &stubProvider{name: "workflow", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "find", Name: "web_lookup", Arguments: `{"query":"an asset"}`}, FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "inspect", Name: invokeToolName, Arguments: `{"tool":"inspect_asset","args":{"url":"https://example.com/asset.bin"}}`}, FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "save", Name: invokeToolName, Arguments: `{"tool":"save_asset","args":{"url":"https://example.com/asset.bin","path":"assets/second.bin"}}`}, FinishReason: "tool_calls"}},
		{{Content: "Saved.", FinishReason: "stop"}},
	}}
	l.provider = p
	events := drainEvents(t, mustRun(t, l, "another one please"))
	for _, event := range events {
		if e, ok := event.(ErrorEvent); ok {
			t.Fatal(e.Err)
		}
	}
	if p.calls != 4 || l.InvokeToolDispatches() != 2 {
		t.Fatalf("model=%d dispatch=%d", p.calls, l.InvokeToolDispatches())
	}
	if !requestContainsToolContract(p.reqs[1], "inspect_asset", p.toolDefsReqs[1]) || !requestContainsToolContract(p.reqs[1], "save_asset", p.toolDefsReqs[1]) {
		t.Fatal("lookup did not hand off exact capabilities")
	}
	for _, m := range l.Messages {
		if strings.Contains(m.Content, requestedToolContextPreamble) {
			t.Fatal("workflow contracts persisted in transcript")
		}
		for _, c := range m.ToolCalls {
			if c.Name == "tool_search" || c.Name == invokeToolName {
				t.Fatal("discovery or unresolved dispatch in history")
			}
		}
	}
}

func TestWorkflowFailedActualCallDoesNotLearnTools(t *testing.T) {
	l := workflowFixture(t, true)
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "web_lookup", Description: "failed search", NextTools: []string{"save_asset"}, Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Err: errors.New("offline")}, nil
		}})
	save, _ := l.registry.Get("save_asset")
	reg.MustRegister(save)
	reg.MarkAlwaysOn("web_lookup")
	l.SetRegistry(reg)
	l.provider = &stubProvider{name: "failure", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "find", Name: "web_lookup", Arguments: `{}`}, FinishReason: "tool_calls"}}, {{Content: "Failed.", FinishReason: "stop"}},
	}}
	drainEvents(t, mustRun(t, l, "another one please"))
	if len(l.workflowTools) != 0 {
		t.Fatal("failed execution learned capabilities")
	}
}

// The source starts hidden and complex, so its envelope cannot resolve before
// tool_search executes. Learn its metadata from the successful execution edge,
// without duplicating work or treating discovery alone as completed work.
func TestWorkflowSameBatchDiscoveryAndInvokeLearnsEffectiveSource(t *testing.T) {
	for _, mode := range []string{"success", "failed", "inert", "malformed envelope", "invalid target args", "search only"} {
		t.Run(mode, func(t *testing.T) {
			reg := tools.NewRegistry()
			sourceExecutions, nextExecutions := 0, 0
			reg.MustRegister(tools.Tool{
				Name: "select_assets", Description: "Select fixture assets", NextTools: []string{"finish_selection"},
				Schema: `{"type":"object","properties":{"items":{"type":"array","items":{"type":"string"}}},"required":["items"],"additionalProperties":false}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					sourceExecutions++
					if mode == "failed" {
						return tools.Result{Err: errors.New("fixture source failed")}, nil
					}
					return tools.Result{Text: "Selected assets", Inert: mode == "inert"}, nil
				},
			})
			reg.MustRegister(tools.Tool{
				Name: "finish_selection", Description: "Complete fixture selection",
				Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					nextExecutions++
					return tools.Result{Text: "Completed selection"}, nil
				},
			})
			reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
			reg.MarkAlwaysOn("tool_search")
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn(invokeToolName)
			envelope := `{"tool":"select_assets","args":{"items":["a","b"]}}`
			if mode == "malformed envelope" {
				envelope = `{"tool":"select_assets","args":{"items":["a"]},"items":["b"]}`
			}
			if mode == "invalid target args" {
				envelope = `{"tool":"select_assets","args":{}}`
			}
			if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: invokeToolName, Arguments: envelope}); err == nil {
				t.Fatal("complex source was already callable before discovery")
			}
			first := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: `{"query":"select_assets"}`}}}
			if mode != "search only" {
				first = append(first, llm.Delta{ToolCall: &llm.ToolCall{ID: "source", Name: invokeToolName, Arguments: envelope}})
			}
			scripts := [][]llm.Delta{first}
			if mode == "success" {
				scripts = append(scripts, []llm.Delta{{ToolCall: &llm.ToolCall{ID: "next", Name: invokeToolName, Arguments: `{"tool":"finish_selection","args":{"path":"selected.bin"}}`}}})
			}
			scripts = append(scripts, []llm.Delta{{Content: "Finished.", FinishReason: "stop"}})
			provider := &stubProvider{name: "discovery-workflow", scripts: scripts}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: true, StableToolset: true, MaxSteps: 4})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Continue the fixture task.")) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			wantSource := 1
			if mode == "search only" || mode == "malformed envelope" || mode == "invalid target args" {
				wantSource = 0
			}
			wantNext, wantRequests := 0, int32(2)
			if mode == "success" {
				wantNext, wantRequests = 1, 3
			}
			if sourceExecutions != wantSource || nextExecutions != wantNext || provider.calls != wantRequests {
				t.Fatalf("executions=%d/%d requests=%d; want %d/%d requests=%d", sourceExecutions, nextExecutions, provider.calls, wantSource, wantNext, wantRequests)
			}
			if requestContainsToolContract(provider.reqs[1], "finish_selection") != (mode == "success") {
				t.Fatal("workflow contract did not follow the actual successful source")
			}
			if reg.IsActive("finish_selection") {
				t.Fatal("handoff became persistent discovery")
			}
		})
	}
}
