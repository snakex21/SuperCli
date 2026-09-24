package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Historical behavior is the compatibility oracle: capacity may change, but
// message order, placeholders, Parts and tool-pair payloads may not.
func referenceVisibleHistory(messages []llm.Message, hidden []bool) []llm.Message {
	if hidden == nil {
		return messages
	}
	out := make([]llm.Message, 0, len(messages))
	runStart := -1
	flush := func(end int) {
		if runStart >= 0 {
			out = append(out, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("[earlier context cleared — %d message(s) compacted]", end-runStart)})
			runStart = -1
		}
	}
	for i, m := range messages {
		if i < len(hidden) && hidden[i] {
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		flush(i)
		out = append(out, m)
	}
	flush(len(messages))
	return out
}

func visibleBufferFixture(n int) []llm.Message {
	out := make([]llm.Message, n)
	for i := range out {
		out[i] = llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("entry-%d ", i) + strings.Repeat("project context ", 10)}
	}
	return out
}

func TestVisibleHistoryBufferParity(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "question", Parts: []llm.ContentPart{{Type: llm.PartTypeImage, Image: &llm.ImageRef{Path: "fixture.png", MediaType: "image/png", Active: true}}}},
		{Role: llm.RoleAssistant, Content: "checking", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "call-1", Content: "observed value"},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "answer"}}},
		{Role: llm.RoleUser, Content: "next"},
	}
	original, _ := json.Marshal(messages)
	for length := 0; length <= len(messages)+2; length++ {
		for mask := 0; mask < 1<<length; mask++ {
			hidden := make([]bool, length)
			for i := range hidden {
				hidden[i] = mask&(1<<i) != 0
			}
			loop := &Loop{Messages: messages, hidden: hidden}
			got, want := loop.VisibleMessages(), referenceVisibleHistory(messages, hidden)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("length=%d mask=%d", length, mask)
			}
			if llm.EstimateTokens(got) != llm.EstimateTokens(want) {
				t.Fatal("estimate changed")
			}
		}
	}
	for _, messages := range [][]llm.Message{nil, {}} {
		for _, hidden := range [][]bool{nil, {}, {true}, {false}} {
			got := (&Loop{Messages: messages, hidden: hidden}).VisibleMessages()
			if !reflect.DeepEqual(got, referenceVisibleHistory(messages, hidden)) {
				t.Fatal("empty view changed")
			}
		}
	}
	after, _ := json.Marshal(messages)
	if string(original) != string(after) {
		t.Fatal("canonical history mutated")
	}
}

func TestVisibleHistoryBufferAppendsAndRehides(t *testing.T) {
	loop := &Loop{Messages: visibleBufferFixture(100)}
	if err := loop.HideRange(0, 90); err != nil {
		t.Fatal(err)
	}
	old := loop.VisibleMessages()
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "fresh tail"})
	if err := loop.HideRange(89, 95); err != nil {
		t.Fatal(err)
	}
	got := loop.VisibleMessages()
	if !reflect.DeepEqual(got, referenceVisibleHistory(loop.Messages, loop.hidden)) || got[len(got)-1].Content != "fresh tail" {
		t.Fatal("stale projection")
	}
	if old[1].Content != loop.Messages[90].Content {
		t.Fatal("prior view mutated")
	}
	loop.resetHidden()
	if &loop.VisibleMessages()[0] != &loop.Messages[0] {
		t.Fatal("reset lost original view")
	}
}

func TestVisibleHistoryPreparedRequestParity(t *testing.T) {
	for _, thin := range []bool{false, true} {
		loop := contextPreparationFixture(t, thin)
		loop.hidden = make([]bool, len(loop.Messages)-2)
		for i := 1; i < len(loop.hidden)-4; i++ {
			loop.hidden[i] = true
		}
		reference := referenceVisibleHistory(loop.Messages, loop.hidden)
		got := loop.providerMessages()
		estimate := loop.EstimateNextRequestTokens()
		loop.Messages = reference
		loop.hidden = nil
		loop.invalidateVisibleEstimate()
		want := loop.providerMessages()
		// The request timestamp changes independently of history projection.
		// Normalize only that generated tail for stable cross-run comparison.
		if got[len(got)-1].Role != llm.RoleSystem || want[len(want)-1].Role != llm.RoleSystem {
			t.Fatal("missing request timestamp")
		}
		got[len(got)-1].Content = "[fixed timestamp for fixture]"
		want[len(want)-1].Content = "[fixed timestamp for fixture]"
		if !reflect.DeepEqual(got, want) || estimate != loop.EstimateNextRequestTokens() {
			t.Fatalf("prepared request changed: thin=%v", thin)
		}
		raw, _ := json.Marshal(got)
		t.Logf("thin=%v bytes=%d sha256=%x estimate=%d", thin, len(raw), sha256.Sum256(raw), estimate)
	}
}

var visibleBufferSink []llm.Message
var visibleEstimateSink int

func BenchmarkVisibleHistoryBuffer(b *testing.B) {
	for _, kind := range []string{"prefix_1000", "prefix_10000", "groups_1000", "alternating_1000", "none_1000", "clear_flags_1000"} {
		b.Run(kind, func(b *testing.B) {
			n := 1000
			if kind == "prefix_10000" {
				n = 10000
			}
			loop := &Loop{Messages: visibleBufferFixture(n)}
			if kind != "none_1000" {
				loop.hidden = make([]bool, n)
			}
			for i := range loop.hidden {
				switch {
				case strings.HasPrefix(kind, "prefix"):
					loop.hidden[i] = i < n-10
				case kind == "groups_1000":
					loop.hidden[i] = i%10 < 8
				case kind == "alternating_1000":
					loop.hidden[i] = i%2 == 0
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				visibleBufferSink = loop.VisibleMessages()
			}
		})
	}
}

func BenchmarkHiddenRequestPreparation(b *testing.B) {
	for _, thin := range []bool{false, true} {
		b.Run(fmt.Sprint(thin), func(b *testing.B) {
			loop := contextPreparationFixture(b, thin)
			for i := 0; i < 460; i++ {
				loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "Inspect project"}, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("Finding. ", 100)})
			}
			loop.hidden = make([]bool, len(loop.Messages))
			for i := 1; i < len(loop.hidden)-10; i++ {
				loop.hidden[i] = true
			}
			loop.EstimateNextRequestTokens()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				loop.EstimateNextRequestTokens()
				loop.EstimateNextRequestTokens()
				visibleEstimateSink = estimateRequestTokens(loop.providerMessages(), loop.buildToolDefs())
			}
		})
	}
}
