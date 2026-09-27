package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerCompactionUsesItsOwnActivatedTools(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, activate := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%t/activate=%t", thin, activate), func(t *testing.T) {
				root := t.TempDir()
				base := tools.NewRegistry()
				base.MustRegister(tools.NewReadLines(root).Spec())
				base.MustRegister(tools.NewReadMany(root).Spec())
				base.MustRegister(tools.Tool{Name: "edit_docx", Description: "Parent-only document editor.", Schema: "{\"type\":\"object\"}",
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						t.Fatal("parent-only tool must not execute")
						return tools.Result{}, nil
					}})
				base.Activate("edit_docx")
				longReply := llm.Delta{Content: strings.Repeat("Verified review observation.\n", 1000), FinishReason: "stop"}
				summary := llm.Delta{Content: "Goal: review. Done: inspected evidence. Pending: next correction.", FinishReason: "stop"}
				p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
					{longReply}, {longReply}, {longReply}, {summary}, {longReply}, {summary},
				}}
				parent, err := NewLoop(LoopConfig{
					Provider: p, Registry: base, BaseDir: root,
					ThinTools: thin, StableToolset: true, CatalogHoist: thin,
					WindowFor:  func(string) int { return 200_000 },
					Summarizer: NewAutoSummarizer(base.ActiveNames),
				})
				if err != nil {
					t.Fatal(err)
				}
				roles := NewSubAgentRegistry()
				MustRegisterAll(roles, BuiltinSubAgents())
				var child *Loop
				loops := 0
				task, err := NewAgentTool(roles, parent, base, p, nil, func(cfg LoopConfig) (*Loop, error) {
					loops++
					var err error
					child, err = NewLoop(cfg)
					return child, err
				})
				if err != nil {
					t.Fatal(err)
				}
				result, err := task.execute(context.Background(), json.RawMessage("{\"agent\":\"review\",\"prompt\":\"Inspect the evidence and report findings.\"}"))
				if err != nil || result.Err != nil {
					t.Fatalf("task: %v %v", err, result.Err)
				}
				workers := task.Workers.List()
				if len(workers) != 1 {
					t.Fatalf("workers=%d", len(workers))
				}
				follow := func() {
					t.Helper()
					args, _ := json.Marshal(map[string]string{"to": workers[0].ID, "message": "Continue the same review with the available evidence."})
					result, err := NewSendMessageTool(task.Workers).execute(context.Background(), args)
					if err != nil || result.Err != nil {
						t.Fatalf("send_message: %v %v", err, result.Err)
					}
				}
				follow()
				follow()
				if _, ok := child.registry.Get("edit_docx"); ok {
					t.Fatal("review allowlist leaked parent tool")
				}
				child.registry.ResetVisibility()
				if activate {
					child.registry.Activate("read_lines")
				}
				if _, err := child.CompactNow(context.Background()); err != nil {
					t.Fatal(err)
				}
				check := func(want string) {
					t.Helper()
					var compacted string
					for _, message := range child.Messages {
						if strings.HasPrefix(message.Content, compactSummaryPreamble) {
							compacted = message.Content
							break
						}
					}
					if strings.Contains(compacted, "edit_docx") {
						t.Errorf("parent activation leaked into worker summary: %s", compacted)
					}
					if want == "" {
						if strings.Contains(compacted, "loaded_tools:") {
							t.Errorf("empty worker activation fell back to parent: %s", compacted)
						}
					} else if !strings.Contains(compacted, "loaded_tools: "+want) {
						t.Errorf("worker activation missing: %s", compacted)
					}
				}
				if activate {
					check("read_lines")
				} else {
					check("")
				}
				follow()
				child.registry.Deactivate("read_lines")
				child.registry.Activate("read_many")
				if _, err := child.CompactNow(context.Background()); err != nil {
					t.Fatal(err)
				}
				check("read_many")
				if base.IsActive("read_lines") || base.IsActive("read_many") || !base.IsActive("edit_docx") {
					t.Fatal("worker compaction changed parent activations")
				}
				if loops != 1 || len(p.reqs) != 6 {
					t.Fatalf("loops=%d requests=%d; want one worker and four turns plus two summaries", loops, len(p.reqs))
				}
			})
		}
	}
}

func TestCompactionToolScopeAcrossEntryPointsAndFallback(t *testing.T) {
	for _, mode := range []string{"manual", "auto", "model-switch"} {
		for _, fallback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fallback=%t", mode, fallback), func(t *testing.T) {
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewReadLines(t.TempDir()).Spec())
				side := &failingSummaryProvider{}
				main := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{
					Content: "Goal: continue. Done: reviewed evidence. Pending: current request.", FinishReason: "stop",
				}}}, onCalled: func(int) { reg.Activate("read_lines") }}
				var sideProvider llm.Provider
				if fallback {
					sideProvider = side
				}
				parentCallbackCalls := 0
				l := &Loop{
					provider: main, registry: reg, route: RouteCoordinator,
					windowFor: func(string) int { return 3000 },
					summarizer: NewAutoSummarizerWithProvider(sideProvider, func() []string {
						parentCallbackCalls++
						return []string{"parent_only_editor"}
					}),
					Messages: []llm.Message{
						{Role: llm.RoleUser, Content: "old request"},
						{Role: llm.RoleAssistant, Content: strings.Repeat("verified observation ", 1000)},
						{Role: llm.RoleUser, Content: "previous correction"},
						{Role: llm.RoleAssistant, Content: "previous result"},
						{Role: llm.RoleUser, Content: "current request"},
					},
					contextModel: contextModelState{loaded: true, provider: "old", model: "old"},
				}
				switch mode {
				case "manual":
					if _, err := l.CompactNow(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "auto":
					l.maybeAutoCompact(context.Background(), nil, "")
				case "model-switch":
					if !l.maybeModelHandoff(context.Background(), nil) {
						t.Fatal("handoff not triggered")
					}
				}
				text := RenderCompactTranscript(l.Messages)
				if !strings.Contains(text, "loaded_tools: read_lines") || strings.Contains(text, "parent_only_editor") || parentCallbackCalls != 0 {
					t.Fatalf("wrong registry scope (parent callback calls=%d): %.600s", parentCallbackCalls, text)
				}
				if len(main.reqs) != 1 || (fallback && side.calls.Load() != 1) || (!fallback && side.calls.Load() != 0) {
					t.Fatalf("main requests=%d side requests=%d", len(main.reqs), side.calls.Load())
				}
			})
		}
	}
}

func TestCompactionExplicitEmptyScopeDoesNotInheritCallback(t *testing.T) {
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "Goal: inspect. Done: findings.", FinishReason: "stop"}}}}
	callback := func() []string { return []string{"standalone_tool"} }
	summarizer := NewAutoSummarizer(callback)
	messages := []llm.Message{{Role: llm.RoleUser, Content: "inspect"}}
	standalone, err := summarizer(context.Background(), p, messages)
	if err != nil || !strings.Contains(standalone, "loaded_tools: standalone_tool") {
		t.Fatalf("standalone callback lost: %s %v", standalone, err)
	}
	l := &Loop{provider: p, summarizer: summarizer}
	empty, err := l.summarizePrefix(context.Background(), messages)
	if err != nil || strings.Contains(empty, "loaded_tools:") {
		t.Fatalf("empty loop inherited standalone callback: %s %v", empty, err)
	}
}
