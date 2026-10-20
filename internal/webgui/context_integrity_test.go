package webgui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/prompt"
)

func TestContextIntegrityPreflightUsesConversationInput(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Source: llm.SourceHuman, Text: "hello"}}},
		{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "Inspect all project files and change them."},
	}
	if !shouldQueuePreflight(history) {
		t.Fatal("a multipart greeting or worker report consumed the first project preflight")
	}
	history = append(history, llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "Fix the <task-notification> parser in this project."}}))
	if shouldQueuePreflight(history) {
		t.Fatal("a genuine project request was ignored because it quoted a worker tag")
	}
}

func TestContextIntegrityKnownHumanReportQuoteStaysHuman(t *testing.T) {
	quoted := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: legacyWorkerNotice}})
	if isTranscriptWorkerNotification(quoted) {
		t.Fatal("known human text was relabeled by a legacy report-shaped envelope")
	}
}

type contextIntegrityDelegationProvider struct {
	requests [][]llm.Message
}

func (*contextIntegrityDelegationProvider) Name() string { return "echo-context-integrity" }
func (p *contextIntegrityDelegationProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.requests = append(p.requests, messages)
	stream := make(chan llm.Delta, 1)
	if len(p.requests) == 1 {
		stream <- llm.Delta{ToolCall: &llm.ToolCall{ID: "worker", Name: "task", Arguments: `{"agent":"code","prompt":"Inspect the assigned parser and report."}`}, FinishReason: "tool_calls"}
	} else {
		stream <- llm.Delta{Content: "Fixture complete.", FinishReason: "stop"}
	}
	close(stream)
	return stream, nil
}

func TestContextIntegrityGUIWorkerReceivesCurrentPolicies(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "preset switched", false: "preset disabled"}[enabled], func(t *testing.T) {
			home, data := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("PROJECT_POLICY_GUI: preserve the existing API."), 0o600); err != nil {
				t.Fatal(err)
			}
			state := prompt.UserInstructionsState{Enabled: true, ActiveID: "fixture", Presets: []prompt.UserInstructionPreset{{ID: "fixture", Name: "Fixture", Content: "STALE_PRESET_A"}}}
			if _, err := prompt.SaveUserInstructions(data, state); err != nil {
				t.Fatal(err)
			}
			engine, err := NewEngine(echoConfig(), home, data)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			provider := &contextIntegrityDelegationProvider{}
			engine.prov = provider
			loop, err := engine.newLoop()
			if err != nil {
				t.Fatal(err)
			}
			state.Enabled = enabled
			state.Presets[0].Content = "CURRENT_PRESET_B: portable files; no publishing."
			if _, err := prompt.SaveUserInstructions(data, state); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events, err := loop.Run(ctx, "Inspect the parser using a code worker.")
			if err != nil {
				t.Fatal(err)
			}
			for event := range events {
				if failure, ok := event.(agent.ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if len(provider.requests) != 3 {
				t.Fatalf("model invocations=%d, want main/worker/main", len(provider.requests))
			}
			for i, messages := range provider.requests {
				prefix := messages[0].Content
				if strings.Contains(prefix, "STALE_PRESET_A") || strings.Contains(prefix, "CURRENT_PRESET_B") != enabled || strings.Count(prefix, "PROJECT_POLICY_GUI") != 1 {
					t.Fatalf("request %d lost, duplicated or retained the wrong policy", i)
				}
			}
		})
	}
}
