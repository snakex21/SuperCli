package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestCanonicalWorkerReportExactStorageReuse(t *testing.T) {
	text := strings.Repeat("verified report; ", 64)
	block := &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: json.RawMessage("{\"reasoning_content\":\"opaque\"}")}
	for _, parts := range []bool{false, true} {
		name := "content"
		if parts {
			name = "native_reasoning_and_text"
		}
		t.Run(name, func(t *testing.T) {
			stored := " \n" + text + "\t\n"
			message := llm.Message{Role: llm.RoleAssistant, Content: stored}
			if parts {
				message.Content = "ignored content"
				message.Parts = []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: block}, {Type: llm.PartTypeText, Text: stored}}
			}
			loop := &Loop{Messages: []llm.Message{{Role: llm.RoleUser, Content: "task"}, message}}
			before, _ := json.Marshal(loop.Messages)
			report := strings.Clone(strings.TrimSpace(stored))
			canonical := strings.TrimSpace(stored)
			if unsafe.StringData(report) == unsafe.StringData(canonical) {
				t.Fatal("fixture failed to create independent report storage")
			}
			got := canonicalWorkerReport(loop, report)
			if got != report || unsafe.StringData(got) != unsafe.StringData(canonical) {
				t.Fatal("equal report was not shared exactly")
			}
			after, _ := json.Marshal(loop.Messages)
			if string(before) != string(after) {
				t.Fatal("canonicalization changed history or native continuation state")
			}
		})
	}
}

func TestCanonicalWorkerReportPreservesFallbackStorage(t *testing.T) {
	text := strings.Repeat("exact report ", 16)
	older := llm.Message{Role: llm.RoleAssistant, Content: strings.Clone(text)}
	cases := []struct {
		name     string
		messages []llm.Message
		report   string
	}{
		{name: "no history", report: text},
		{name: "no assistant", messages: []llm.Message{{Role: llm.RoleUser, Content: text}}, report: text},
		{name: "different newest reply", messages: []llm.Message{older, {Role: llm.RoleAssistant, Content: "new reply"}}, report: text},
		{name: "newest tool call", messages: []llm.Message{older, {Role: llm.RoleAssistant, Content: text, ToolCalls: []llm.ToolCall{{ID: "read", Name: "read_lines", Arguments: "{}"}}}}, report: text},
		{name: "multiple text parts", messages: []llm.Message{{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: text}, {Type: llm.PartTypeText, Text: ""}}}}, report: text},
		{name: "image part", messages: []llm.Message{{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: text}, {Type: llm.PartTypeImage, Image: &llm.ImageRef{URL: "fixture"}}}}}, report: text},
		{name: "unknown part", messages: []llm.Message{{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: text}, {Type: "unknown"}}}}, report: text},
		{name: "reasoning only", messages: []llm.Message{{Role: llm.RoleAssistant, Content: text, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning}}}}, report: text},
		{name: "parts override content", messages: []llm.Message{{Role: llm.RoleAssistant, Content: text, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "other reply"}}}}, report: text},
		{name: "thinking mismatch", messages: []llm.Message{{Role: llm.RoleAssistant, Content: "<thinking>reason</thinking>" + text}}, report: text},
		{name: "steering diagnostic suffix", messages: []llm.Message{older}, report: text + "\n[Steering delivery: rejected]"},
		{name: "report whitespace is not transformed", messages: []llm.Message{older}, report: " " + text},
		{name: "empty report", messages: []llm.Message{older}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loop := &Loop{Messages: tc.messages}
			before, _ := json.Marshal(loop.Messages)
			report := strings.Clone(tc.report)
			got := canonicalWorkerReport(loop, report)
			if got != report || unsafe.StringData(got) != unsafe.StringData(report) {
				t.Fatal("non-equivalent report storage changed")
			}
			after, _ := json.Marshal(loop.Messages)
			if string(before) != string(after) {
				t.Fatal("fallback modified history")
			}
		})
	}
	report := strings.Clone(text)
	if got := canonicalWorkerReport(nil, report); got != report || unsafe.StringData(got) != unsafe.StringData(report) {
		t.Fatal("nil loop changed result")
	}
}

func TestWorkerSharesFinishedReportAndPreservesContinuation(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "text"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			report := strings.Repeat("verified finding; ", 4096)
			script := []llm.Delta{}
			if native {
				script = append(script, llm.Delta{NativeReasoning: &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: json.RawMessage("{\"reasoning_content\":\"opaque continuation\"}")}})
			}
			for offset := 0; offset < len(report); offset += 2048 {
				script = append(script, llm.Delta{Content: report[offset:min(offset+2048, len(report))]})
			}
			script = append(script, llm.Delta{FinishReason: "stop"})
			provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{script, {{Content: "continuation verified", FinishReason: "stop"}}}}
			base := tools.NewRegistry()
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, nil, base, provider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			task.NewLoop = func(cfg LoopConfig) (*Loop, error) {
				cfg.WindowFor = func(string) int { return 1024 * 1024 }
				return NewLoop(cfg)
			}
			result, err := task.execute(context.Background(), json.RawMessage("{\"prompt\":\"Produce the fixture report.\"}"))
			if err != nil || result.Err != nil {
				t.Fatalf("worker failed: %v / %v", err, result.Err)
			}
			worker, exists := task.Workers.Get("worker-1")
			if !exists || worker.Loop.writer != nil || worker.status() != "done" {
				t.Fatal("worker residency or writer changed")
			}
			expected := strings.TrimSpace(report)
			if worker.LastResult != expected {
				t.Fatal("final report value changed")
			}
			final := worker.Loop.Messages[len(worker.Loop.Messages)-1]
			canonical := strings.TrimSpace(final.Content)
			for _, part := range final.Parts {
				if part.Type == llm.PartTypeText {
					canonical = strings.TrimSpace(part.Text)
				}
			}
			if canonical != expected || unsafe.StringData(worker.LastResult) != unsafe.StringData(canonical) {
				t.Fatal("production worker retained separate report bytes")
			}
			before := append([]llm.Message(nil), worker.Loop.Messages...)
			raw, _ := json.Marshal(sendMessageArgs{To: worker.ID, Message: "Continue from previous findings."})
			continued, err := NewSendMessageTool(task.Workers).execute(context.Background(), raw)
			if err != nil || continued.Err != nil || !strings.Contains(continued.Text, "continuation verified") {
				t.Fatalf("continuation failed: %v / %v", err, continued.Err)
			}
			if len(provider.reqs) != 2 || worker.Snapshot().Runs != 2 || len(task.Workers.List()) != 1 {
				t.Fatal("continuation restarted worker or added a provider call")
			}
			if !reflect.DeepEqual(before, worker.Loop.Messages[:len(before)]) {
				t.Fatal("follow-up modified prior history")
			}
			found := false
			for _, message := range provider.reqs[1] {
				if message.Role == llm.RoleAssistant && reflect.DeepEqual(message, final) {
					found = true
				}
			}
			if !found {
				t.Fatal("continuation request lost full report or native state")
			}
		})
	}
}

func TestFailedWorkerDoesNotAliasCoincidentallyEqualOldReply(t *testing.T) {
	report := strings.Repeat("partial finding; ", 128)
	prior := strings.Clone(strings.TrimSpace(report))
	cause := errors.New("fixture stream interrupted")
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: report}, {Err: cause}}}}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), InitialMessages: []llm.Message{{Role: llm.RoleAssistant, Content: prior}}})
	if err != nil {
		t.Fatal(err)
	}
	worker := &Worker{ID: "fixture-worker", Loop: loop}
	result, err := runWorkerLoop(context.Background(), worker, "Repeat findings then encounter an error.")
	if !errors.Is(err, cause) || worker.status() != "failed" || worker.LastError != cause.Error() || result != prior || worker.LastResult != prior {
		t.Fatal("partial report or failure diagnostics changed")
	}
	if unsafe.StringData(worker.LastResult) == unsafe.StringData(prior) {
		t.Fatal("failed report was canonicalized against an older reply")
	}
}
