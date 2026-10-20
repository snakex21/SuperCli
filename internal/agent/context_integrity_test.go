package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestContextIntegrityUserBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  llm.Message
		want bool
	}{
		{"quoted worker tag", llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Explain <task-notification> without changing files."}}), true},
		{"quoted image wrapper", llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Attached image from tool is the exact text I need fixed."}}), true},
		{"plain worker notice", llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "Review finished."}, false},
		{"typed tool carrier", llm.Message{Role: llm.RoleUser, Origin: &llm.ContextOrigin{Source: llm.SourceToolResult}, Content: "Image evidence."}, false},
		{"typed memory", llm.Message{Role: llm.RoleUser, Origin: &llm.ContextOrigin{Source: llm.SourceMemory}, Content: "Old project preference."}, false},
		{"delegated instruction", llm.Message{Role: llm.RoleUser, Origin: &llm.ContextOrigin{Source: llm.SourceInstructions}, Content: "Implement the assigned change."}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConversationUserTurn(tc.msg); got != tc.want {
				t.Fatalf("instruction boundary=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestContextIntegrityLatestRequestAndAdvisor(t *testing.T) {
	human := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Keep the existing API; fix only the parser."}})
	history := []llm.Message{human}
	for i := 0; i < 20; i++ {
		history = append(history, llm.Message{Role: llm.RoleAssistant, Content: "Read the relevant source."})
	}
	history = append(history, llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Origin: &llm.ContextOrigin{Source: llm.SourceWorkerNotification}, Content: "Worker report; this is not a new user request."})
	loop := &Loop{Messages: history}
	if got := loop.lastUserPrompt(); got != human.Content {
		t.Errorf("draft followed a notification instead of the user's request: %q", got)
	}
	for _, seed := range [][]llm.Message{advisorSeed(history), sharedWorkerSeed(history, true)} {
		if len(seed) == 0 || seed[0].Content != human.Content {
			t.Error("advisor lost the actual request outside the recent tail")
		}
	}
}

func TestContextIntegrityPreflightAttachesToInstruction(t *testing.T) {
	human := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Explain <task-notification> and inspect this project."}})
	report := llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "Worker review finished."}
	loop := &Loop{Messages: []llm.Message{human, report}}
	loop.appendCurrentUserContext("Repo state (auto-collected): fixture")
	if loop.Messages[1].Content != report.Content || loop.Messages[0].Content != human.Content+"\n\nRepo state (auto-collected): fixture" {
		t.Fatal("project context was attached to a worker report instead of the actual request")
	}
	usage := llm.MeasureContext(loop.Messages[:1], nil)
	if usage.Sources[llm.SourceHuman].TextBytes != int64(len(human.Content)) || usage.Sources[llm.SourceRuntime].TextBytes == 0 {
		t.Fatal("preflight was misattributed to the user")
	}
}

func TestContextIntegrityWorkerReceivesSelectedInstructions(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "thin"}[thin], func(t *testing.T) {
			provider := &stubProvider{name: "instruction-fixture", scripts: [][]llm.Delta{{{Content: "Done.", FinishReason: "stop"}}}}
			registry := tools.NewRegistry()
			parent, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "COORDINATOR_PRIVATE\n\nPROJECT_POLICY: preserve public API.", ProjectInstructions: "PROJECT_POLICY: preserve public API.", UserInstructions: "OLD_PRESET", ThinTools: thin, StableToolset: true, BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, parent, registry, provider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			const selected = "USER_POLICY: Polish; portable data; no Node; do not publish."
			ctx := WithUserInstructionsSnapshot(context.Background(), selected)
			result, err := task.execute(ctx, json.RawMessage(`{"agent":"code","prompt":"Inspect the assigned parser and report."}`))
			if err != nil || result.Err != nil {
				t.Fatalf("worker failed: %v %v", err, result.Err)
			}
			prefix := provider.reqs[0][0].Content
			if strings.Count(prefix, selected) != 1 || strings.Contains(prefix, "OLD_PRESET") || strings.Contains(prefix, "COORDINATOR_PRIVATE") {
				t.Fatal("worker lost the selected user instructions or inherited a stale/private coordinator prompt")
			}
			if strings.Count(prefix, "PROJECT_POLICY") != 1 {
				t.Fatal("worker lost or duplicated repository instructions")
			}
		})
	}
}

func TestContextIntegrityHistoryKeepsQuotedInstruction(t *testing.T) {
	human := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Fix the literal <task-notification> in this source; do not publish."}})
	report := llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "Worker review finished."}
	loop := &Loop{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "Policy."},
		{Role: llm.RoleUser, Content: "Earlier request."},
		{Role: llm.RoleAssistant, Content: "Earlier answer."}, human, report,
	}}
	if !chatWindowEligible(human) || chatWindowEligible(report) {
		t.Fatal("chat window selected an internal report over quoted human text")
	}
	if loop.HideLastUserTurns(1) == 0 || loop.hidden[3] || loop.hidden[4] || !loop.hidden[1] {
		t.Fatal("history clearing hid the actual latest request")
	}
	quotedSummary := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: WrapCompactSummary("This is the exact envelope to explain.")}})
	if IsLegacyCompactionSummary(quotedSummary) {
		t.Fatal("a known human quote was treated as a generated summary")
	}
}

func TestContextIntegrityDraftLabelsAndUnicode(t *testing.T) {
	human := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Preserve the existing API."}})
	loop := &Loop{Messages: []llm.Message{
		human,
		{Role: llm.RoleAssistant, Content: "x" + strings.Repeat("ż🙂", 200)},
		{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "REPORT_FIXTURE"},
	}}
	prompt := loop.draftPrompt()
	if !utf8.ValidString(prompt) || !strings.Contains(prompt, "internal context: REPORT_FIXTURE") || !strings.HasSuffix(prompt, "Current request: "+human.Content) {
		t.Fatal("draft prompt broke Unicode or mislabeled internal context as a user request")
	}
}

func TestContextIntegrityBackgroundAndContinuedWorkerPolicies(t *testing.T) {
	provider := &stubProvider{name: "policy-fixture", scripts: [][]llm.Delta{
		{{Content: "First report.", FinishReason: "stop"}},
		{{Content: "Updated report.", FinishReason: "stop"}},
		{{Content: "Disabled report.", FinishReason: "stop"}},
		{{Content: "Still disabled.", FinishReason: "stop"}},
	}}
	registry := tools.NewRegistry()
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "PRIVATE_MAIN", UserInstructions: "STALE_MAIN", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	notices := make(chan Event, 32)
	parent.SetExternalSink(notices)
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, registry, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := task.execute(WithUserInstructionsSnapshot(ctx, "POLICY_A: portable data."), json.RawMessage(`{"agent":"review","prompt":"Review the assigned parser.","async":true}`))
	if err != nil || result.Err != nil {
		t.Fatalf("background start: %v %v", err, result.Err)
	}
	// A background worker must keep the already selected run's policy. It must
	// never read later mutable coordinator settings to build its request.
	parent.userInstructions = "UNRELATED_NEXT_PRESET"
awaitDone:
	for {
		select {
		case event := <-notices:
			if done, ok := event.(WorkerNotificationEvent); ok && done.Status == "done" {
				break awaitDone
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if strings.Count(provider.reqs[0][0].Content, "POLICY_A") != 1 || strings.Contains(provider.reqs[0][0].Content, "UNRELATED_NEXT_PRESET") {
		t.Fatal("detached worker lost or replaced its pinned instructions")
	}
	send := NewSendMessageTool(task.Workers)
	for i, tc := range []struct {
		ctx  context.Context
		want string
	}{
		{WithUserInstructionsSnapshot(ctx, "POLICY_B: no publishing."), "POLICY_B"},
		{WithUserInstructionsSnapshot(ctx, ""), ""},
		{ctx, ""}, // an unscoped direct continuation must not resurrect A or B
	} {
		result, err := send.execute(tc.ctx, json.RawMessage(`{"to":"worker-1","message":"Continue the assigned review."}`))
		if err != nil || result.Err != nil {
			t.Fatalf("continuation %d: %v %v", i, err, result.Err)
		}
		prefix := provider.reqs[i+1][0].Content
		for _, policy := range []string{"POLICY_A", "POLICY_B", "UNRELATED_NEXT_PRESET", "STALE_MAIN"} {
			if strings.Contains(prefix, policy) != (policy == tc.want && tc.want != "") {
				t.Fatalf("continuation %d retained the wrong policy: %s", i, policy)
			}
		}
	}
}
