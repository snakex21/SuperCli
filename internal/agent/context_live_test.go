package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/memory"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

func liveRequestText(messages []llm.Message) string {
	var text strings.Builder
	for _, message := range messages {
		text.WriteString(message.TextOnly().Content)
	}
	return text.String()
}

func TestLiveContextBlocksUnchangedReadsDoNotAccumulateOrRefresh(t *testing.T) {
	refreshes := 0
	loop, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(), System: "Fixed policy",
		LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
			refreshes++
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: "MEMORY_FIXTURE zażółć 🙂 東京"}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.refreshLiveContextBoundary(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	history := append([]llm.Message(nil), loop.Messages...)
	for i := 0; i < 100; i++ {
		loop.markLiveContextAfterTools([]llm.ToolCall{{Name: "read_lines"}, {Name: "goal", Arguments: `{"action":"show"}`}}, []callOutcome{{}, {}})
		if err := loop.refreshLiveContextBoundary(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(liveRequestText(loop.providerMessages()), "MEMORY_FIXTURE"); got != 1 {
			t.Fatalf("request %d contains %d memory snapshots", i, got)
		}
	}
	if refreshes != 1 || !reflect.DeepEqual(history, loop.Messages) {
		t.Fatalf("refreshes=%d; canonical history changed=%t", refreshes, !reflect.DeepEqual(history, loop.Messages))
	}
	t.Log("100 ordinary batches + 100 prepared views: 1 host refresh; 1 memory snapshot/request; 0 persisted memory turns; 0 model calls")
}

func TestLiveContextBlocksRepeatedRefreshCountsOnlyChangedSourceVersion(t *testing.T) {
	text := "CONSTANT_AGREEMENT"
	loop, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(), Stats: stats.NewMemory(),
		LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: text}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := loop.refreshLiveContextBoundary(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}
	if got := loop.pendingContextUsage.Sources[llm.SourceMemory].AddedItems; got != 1 {
		t.Fatalf("100 unchanged refreshes counted %d memory additions", got)
	}
	text = "UPDATED_AGREEMENT"
	if err := loop.refreshLiveContextBoundary(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := loop.pendingContextUsage.Sources[llm.SourceMemory].AddedItems; got != 2 {
		t.Fatalf("real change counted %d additions, want 2", got)
	}
	loop.conversationEpoch.Add(1)
	if err := loop.refreshLiveContextBoundary(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := loop.pendingContextUsage.Sources[llm.SourceMemory].AddedItems; got != 1 {
		t.Fatalf("new epoch inherited previous additions or lost its required snapshot: %d", got)
	}
	t.Log("100 identical refresh attempts: 1 source/version addition; updated version: +1; new conversation epoch: scoped addition resets")
}

func TestLiveContextMemoryMutationReachesNextRequestAfterWholeBatch(t *testing.T) {
	store, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := tools.NewRegistry()
	registry.MustRegister(tools.NewRemember(store).Spec())
	registry.MarkAlwaysOn("remember")
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "memory-one", Name: "remember", Arguments: `{"type":"fact","text":"Unique fixture agreement: preserve Polish characters zażółć and emoji 🙂."}`}},
			{ToolCall: &llm.ToolCall{ID: "memory-two", Name: "remember", Arguments: `{"type":"fact","text":"Separate fixture agreement: preserve 東京 symbols in generated reports."}`}, FinishReason: "tool_calls"}},
		{{Content: "Saved both agreements.", FinishReason: "stop"}},
	}}
	refreshes := 0
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Fixture policy", SkipImplementationHint: true,
		LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
			refreshes++
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: memory.BuildBriefing(nil, store, "fixture", 700)}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, mustRun(t, loop, "Save both independent fixture agreements."))
	if len(provider.reqs) != 2 || refreshes != 2 {
		t.Fatalf("requests=%d refreshes=%d; want exactly 2 each", len(provider.reqs), refreshes)
	}
	first, second := liveRequestText(provider.reqs[0]), liveRequestText(provider.reqs[1])
	// The literals occur as tool arguments/results in history after execution.
	// Inspect only the request-only final snapshot for freshness and uniqueness.
	tail := provider.reqs[1][len(provider.reqs[1])-1].Content
	if strings.Contains(first, "[memory_briefing]") || strings.Count(tail, "Unique fixture agreement:") != 1 || strings.Count(tail, "Separate fixture agreement:") != 1 {
		t.Fatalf("incorrect before/after snapshots: first=%t, second bytes=%d", strings.Contains(first, "[memory_briefing]"), len(second))
	}
	if !strings.Contains(tail, "zażółć") || !strings.Contains(tail, "🙂") || !strings.Contains(tail, "東京") {
		t.Fatal("refresh lost unique Unicode agreements")
	}
	for _, message := range loop.Messages {
		if strings.Contains(message.Content, "[memory_briefing]") {
			t.Fatal("automatic snapshot entered canonical history")
		}
	}
	// A saved provider request is frozen even after the loop refreshes its tail.
	if strings.Contains(liveRequestText(provider.reqs[0]), "[memory_briefing]") {
		t.Fatal("refresh mutated a prior provider request")
	}
	t.Log("2 actual memory writes in 1 batch: 1 boundary refresh (2 including startup); next request contains both facts; 2 model calls, no dedup inference")
}

func TestLiveContextGoalTerminalStateRemovedFromNextRequest(t *testing.T) {
	for _, fixture := range []struct{ name, action, tool string }{
		{"abandon", "abandon", "goal"}, {"mark_done", "mark_done", "goal"},
		{"invoke_abandon", "abandon", "invoke_tool"}, {"wrapped_abandon", "abandon", "functions.goal"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			goals := goal.NewStorage(db)
			if err := goals.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			svc := goal.NewProjectService(goals, "fixture-root")
			if _, err := svc.Set(ctx, "CURRENT_FIXTURE_GOAL", "", "", ""); err != nil {
				t.Fatal(err)
			}
			if fixture.action == "mark_done" {
				if err := svc.Verify(ctx, "", true, "Fixture completed without implementation changes."); err != nil {
					t.Fatal(err)
				}
			}
			registry := tools.NewRegistry()
			registry.MustRegister(tools.NewGoalTool(svc).Spec())
			registry.MarkAlwaysOn("goal")
			registry.MustRegister(NewInvokeTool(registry).Spec())
			registry.MarkAlwaysOn("invoke_tool")
			args, _ := json.Marshal(map[string]string{"action": fixture.action})
			if fixture.tool == "invoke_tool" {
				args, _ = json.Marshal(map[string]any{"tool": "goal", "args": json.RawMessage(args)})
			}
			provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "close", Name: fixture.tool, Arguments: string(args)}, FinishReason: "tool_calls"}},
				{{Content: "Goal closed.", FinishReason: "stop"}},
			}}
			refreshes := 0
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Fixture policy", SkipImplementationHint: true,
				LiveContextBlocks: func(ctx context.Context) ([]llm.ContextBlock, error) {
					refreshes++
					if _, err := svc.Refresh(ctx); err != nil {
						return nil, err
					}
					current, err := svc.Inject(ctx, "", 5)
					return []llm.ContextBlock{{Source: llm.SourceGoalState, Text: current}}, err
				}})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "Close the current fixture goal."))
			if len(provider.reqs) != 2 || refreshes != 2 {
				t.Fatalf("requests=%d refreshes=%d", len(provider.reqs), refreshes)
			}
			if !strings.Contains(liveRequestText(provider.reqs[0]), "[current_goal]") || strings.Contains(provider.reqs[1][len(provider.reqs[1])-1].Content, "[current_goal]") {
				t.Fatal("terminal goal remains active in the next briefing")
			}
			if svc.Active() != nil {
				t.Fatal("goal tool did not close the active goal")
			}
			t.Log("active-goal blocks 1 -> 0 by the next request after a real terminal action; 2 calls, no extra inference")
		})
	}
}

func TestLiveContextBackgroundCompletionRefreshesAtNextSafeBoundary(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "thin"}[thin], func(t *testing.T) {
			version, refreshes := "OLD_MEMORY_FIXTURE", 0
			registry := tools.NewRegistry()
			var loop *Loop
			registry.MustRegister(tools.Tool{Name: "fixture_read", Description: "Read fixture", ReadOnly: true, Schema: `{"type":"object"}`,
				Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
					// A delegated operation updated shared state while the parent read
					// was pending. Its completion producer queues the invalidation.
					version = "UPDATED_MEMORY_FIXTURE"
					loop.backgroundDelivery(ctx)(ctx, "Worker completed the fixture agreement update.")
					return tools.Result{Text: "Fixture read complete."}, nil
				}})
			registry.MarkAlwaysOn("fixture_read")
			toolDelta := llm.Delta{ToolCall: &llm.ToolCall{ID: "read", Name: "fixture_read", Arguments: "{}"}, FinishReason: "tool_calls"}
			if thin {
				toolDelta = llm.Delta{Content: "«fixture_read»", FinishReason: "stop"}
			}
			provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{toolDelta}, {{Content: "Read and worker update complete.", FinishReason: "stop"}}}}
			var err error
			loop, err = NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Fixed fixture policy", ThinTools: thin, SkipImplementationHint: true,
				LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
					refreshes++
					return []llm.ContextBlock{{Source: llm.SourceMemory, Text: version}}, nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "Read the fixture while the worker finishes."))
			if len(provider.reqs) != 2 || refreshes != 2 {
				t.Fatalf("calls=%d refreshes=%d", len(provider.reqs), refreshes)
			}
			first, second := liveRequestText(provider.reqs[0]), liveRequestText(provider.reqs[1])
			if !strings.Contains(first, "OLD_MEMORY_FIXTURE") || strings.Contains(first, "UPDATED_MEMORY_FIXTURE") || !strings.Contains(second, "UPDATED_MEMORY_FIXTURE") || strings.Contains(second, "OLD_MEMORY_FIXTURE") {
				t.Fatal("background completion either mutated the frozen request or failed to refresh the next one")
			}
		})
	}
}

func TestLiveContextLegacyStartupGoalReproducesStaleBriefing(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goals := goal.NewStorage(db)
	if err := goals.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc := goal.NewProjectService(goals, "fixture-root")
	if _, err := svc.Set(ctx, "LEGACY_ACTIVE_GOAL", "", "", ""); err != nil {
		t.Fatal(err)
	}
	startup, err := svc.Inject(ctx, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	registry.MustRegister(tools.NewGoalTool(svc).Spec())
	registry.MarkAlwaysOn("goal")
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "close", Name: "goal", Arguments: `{"action":"abandon"}`}, FinishReason: "tool_calls"}},
		{{Content: "Goal closed.", FinishReason: "stop"}},
	}}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Fixture policy", LiveContext: startup, SkipImplementationHint: true})
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, mustRun(t, loop, "Abandon this fixture goal."))
	if svc.Active() != nil || len(provider.reqs) != 2 {
		t.Fatalf("terminal action failed or extra inference: active=%t calls=%d", svc.Active() != nil, len(provider.reqs))
	}
	if !strings.Contains(provider.reqs[1][len(provider.reqs[1])-1].Content, "LEGACY_ACTIVE_GOAL") {
		t.Fatal("fixture no longer reproduces the old one-time LiveContext producer")
	}
	t.Log("legacy GUI-style startup snapshot: active goal in DB=0 but next request active-goal blocks=1; 2 calls; reproduces stale producer without editing user state")
}

func TestLiveContextSurvivesCompactionResumeAndClonesHostBlocks(t *testing.T) {
	blocks := []llm.ContextBlock{{Source: llm.SourceMemory, Text: "CRITICAL_FIXTURE_AGREEMENT"}}
	loop, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(), System: "Fixture policy",
		InitialMessages:   []llm.Message{{Role: llm.RoleUser, Content: "old task"}, {Role: llm.RoleAssistant, Content: "old result"}},
		LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) { return blocks, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := loop.refreshLiveContextBoundary(ctx, true); err != nil {
		t.Fatal(err)
	}
	blocks[0].Text = "MUTATED_HOST_SCRATCH"
	for _, mode := range []string{"compact", "resume", "window"} {
		switch mode {
		case "compact":
			loop.CompactWithSummary("Auditable summary of old work.")
		case "resume":
			loop.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: "resumed task"}})
		case "window":
			loop.chatWindowStart = 1
			loop.route = RouteAdvisor
		}
		text := liveRequestText(loop.providerMessages())
		if strings.Count(text, "CRITICAL_FIXTURE_AGREEMENT") != 1 || strings.Contains(text, "MUTATED_HOST_SCRATCH") {
			t.Fatalf("%s lost the required snapshot or retained the host slice", mode)
		}
	}
	blocks[0].Text = "RESUMED_SESSION_CURRENT_AGREEMENT"
	if err := loop.refreshLiveContextBoundary(ctx, true); err != nil {
		t.Fatal(err)
	}
	text := liveRequestText(loop.providerMessages())
	if !strings.Contains(text, blocks[0].Text) || strings.Contains(text, "CRITICAL_FIXTURE_AGREEMENT") {
		t.Fatal("resume refresh retained a stale session snapshot")
	}
}

func TestLiveContextRefreshFailureCannotCallProviderWithStaleState(t *testing.T) {
	fail := false
	provider := &stubProvider{name: "fixture"}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
		if fail {
			return nil, errors.New("fixture refresh failure")
		}
		return []llm.ContextBlock{{Source: llm.SourceGoalState, Text: "old active goal"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.refreshLiveContextBoundary(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	fail = true
	loop.liveContextDirty = true
	out := make(chan Event, 2)
	result := loop.runStep(context.Background(), 0, out, &Usage{}, nil, nil, 4)
	if result != stepAbort || len(provider.reqs) != 0 || !loop.liveContextDirty {
		t.Fatalf("result=%v calls=%d dirty=%t", result, len(provider.reqs), loop.liveContextDirty)
	}
}

func TestLiveContextIdleRefreshRespectsConversationLease(t *testing.T) {
	refreshes := 0
	loop, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(),
		LiveContextBlocks: func(context.Context) ([]llm.ContextBlock, error) {
			refreshes++
			return []llm.ContextBlock{{Source: llm.SourceMemory, Text: "Required idle context"}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	loop.sessionBusy.Store(true)
	if err := loop.RefreshLiveContext(context.Background()); err == nil || refreshes != 0 {
		t.Fatal("idle inspection changed the active request's state")
	}
	loop.sessionBusy.Store(false)
	if err := loop.RefreshLiveContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || !strings.Contains(liveRequestText(loop.providerMessages()), "Required idle context") || loop.sessionBusy.Load() {
		t.Fatal("idle refresh lost required context or retained the lease")
	}
}
