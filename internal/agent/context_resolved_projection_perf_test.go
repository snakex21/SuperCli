package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestResolvedProjectionUnchangedHistory(t *testing.T) {
	image := llm.ContentPart{Type: llm.PartTypeImage, Image: &llm.ImageRef{Path: "fixture.png", MediaType: "image/png", Active: true}}
	active := []llm.Message{
		{Role: llm.RoleSystem, Content: "instructions"},
		{Role: llm.RoleUser, Content: "earlier request"},
		{Role: llm.RoleAssistant, Content: "earlier visible answer"},
		{Role: llm.RoleUser, Content: "current request", Parts: []llm.ContentPart{image}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "live", Name: "ctx_execute", Arguments: `{"command":"check"}`}}},
		{Role: llm.RoleTool, ToolCallID: "live", Name: "ctx_execute", Content: "error: command_failed exit=1\nstderr: actual failure"},
	}
	native := append([]llm.Message(nil), active...)
	native[4].Parts = policyReply("active native state").Parts
	placeholder := append(append([]llm.Message(nil), active...), llm.Message{Role: llm.RoleAssistant, Content: noVisibleAnswerPlaceholder})
	reasoning := append(append([]llm.Message(nil), active...), llm.Message{Role: llm.RoleAssistant, Content: "<thinking>unfinished private reasoning</thinking>"})
	for name, messages := range map[string][]llm.Message{
		"nil": nil, "empty": {}, "dialogue": active[:3], "active": active,
		"native": native, "placeholder": placeholder, "reasoning only": reasoning,
	} {
		t.Run(name, func(t *testing.T) {
			before, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			for _, tracked := range []bool{false, true} {
				got, indices := resolvedToolHistoryProjection(messages, tracked)
				if !reflect.DeepEqual(got, messages) || indices != nil {
					t.Fatal("unchanged history or implicit index map changed")
				}
				if len(messages) > 0 && &got[0] != &messages[0] {
					t.Fatal("unchanged history was needlessly copied")
				}
			}
			after, _ := json.Marshal(messages)
			if string(before) != string(after) {
				t.Fatal("canonical evidence changed")
			}
		})
	}
}

func TestResolvedProjectionNoMaskForDialogueOrActiveTools(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("active=%v", active), func(t *testing.T) {
			messages := projectionScanFixture(80, false, active, false)
			for _, tracked := range []bool{false, true} {
				if allocations := testing.AllocsPerRun(100, func() {
					resolvedProjectionSink, resolvedProjectionIndexSink = resolvedToolHistoryProjection(messages, tracked)
				}); allocations != 0 {
					t.Fatalf("no completed tool batch: %.0f allocations", allocations)
				}
			}
		})
	}
}

func projectionScanFixture(turns int, long, active, completed bool) []llm.Message {
	messages := []llm.Message{{Role: llm.RoleSystem, Content: "instructions"}}
	if completed {
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "old project task"})
		messages = append(messages, completedRead("old", "old.go", "older authoritative result")...)
	}
	answer := "verified answer"
	if long {
		answer = strings.Repeat("Verified CODE and Observed Value; preserve this finding.\n", 120)
	}
	for i := 0; i < turns; i++ {
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "continue"}, llm.Message{Role: llm.RoleAssistant, Content: answer})
	}
	if active {
		messages = append(messages,
			llm.Message{Role: llm.RoleUser, Content: "current task"},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "live", Name: "ctx_execute", Arguments: "{}"}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: "live", Name: "ctx_execute", Content: "error: command_failed exit=1\nstderr: actual failure"},
		)
	}
	return messages
}

var resolvedProjectionSink []llm.Message
var resolvedProjectionIndexSink []int

func BenchmarkResolvedProjectionScan(b *testing.B) {
	for _, tc := range []struct {
		name                    string
		turns                   int
		long, active, completed bool
	}{
		{"short_80", 40, false, false, false},
		{"long_80", 40, true, false, false},
		{"long_800", 400, true, false, false},
		{"active_800", 400, true, true, false},
		{"completed_800", 400, true, true, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			messages := projectionScanFixture(tc.turns, tc.long, tc.active, tc.completed)
			b.ReportAllocs()
			for b.Loop() {
				resolvedProjectionSink, resolvedProjectionIndexSink = resolvedToolHistoryProjection(messages, false)
			}
		})
	}
}
