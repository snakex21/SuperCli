package agent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Keep the old preparation path as the oracle, including nil versus empty,
// its shallow copy, advisorSeed's role handling, and UTF-8 clipping policy.
func referenceSharedWorkerSeed(messages []llm.Message, advisor bool) []llm.Message {
	var seed []llm.Message
	for _, message := range messages {
		if message.Role != llm.RoleSystem {
			seed = append(seed, message)
		}
	}
	if advisor {
		seed = advisorSeed(seed)
	}
	return seed
}

func TestSharedWorkerSeedMatchesOldPath(t *testing.T) {
	rng := rand.New(rand.NewSource(73517))
	roles := []llm.Role{llm.RoleSystem, llm.RoleUser, llm.RoleAssistant, llm.RoleTool, "", "other"}
	contents := []string{"", "short", strings.Repeat("x", advisorSeedToolBytes), strings.Repeat("x", advisorSeedToolBytes+1), strings.Repeat("ź🙂", 600)}
	histories := [][]llm.Message{nil, {}, {{Role: llm.RoleSystem}}, {{Role: llm.RoleTool, Content: contents[4]}}, {{Role: llm.RoleUser}, {Role: llm.RoleSystem}, {Role: llm.RoleTool}}}
	// Exhaust all short role permutations, then cover long histories containing
	// interleaved systems, tool pairs/orphans, multimodal tools and long UTF-8.
	for n := 0; n < 1296; n++ {
		value := n
		history := make([]llm.Message, 4)
		for i := range history {
			history[i] = llm.Message{Role: roles[value%len(roles)], Content: contents[i]}
			value /= len(roles)
		}
		histories = append(histories, history)
	}
	for n := 0; n < 400; n++ {
		length := rng.Intn(257)
		if n < 20 {
			length = n // explicitly surround the 12-message threshold
		}
		history := make([]llm.Message, length)
		for i := range history {
			m := llm.Message{Role: roles[rng.Intn(len(roles))], Content: contents[rng.Intn(len(contents))], Name: "tool", ToolCallID: fmt.Sprintf("call-%d", i/2)}
			if rng.Intn(4) == 0 {
				m.Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: "preserve structured payload"}}
			}
			if rng.Intn(3) == 0 {
				m.ToolCalls = []llm.ToolCall{{ID: m.ToolCallID, Name: "read_lines", Arguments: `{"file":"test.go"}`}}
			}
			history[i] = m
		}
		histories = append(histories, history)
	}
	for n, messages := range histories {
		before, err := json.Marshal(messages)
		if err != nil {
			t.Fatal(err)
		}
		for _, advisor := range []bool{false, true} {
			want := referenceSharedWorkerSeed(messages, advisor)
			got := sharedWorkerSeed(messages, advisor)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("history %d advisor=%t differs from old path:\ngot %#v\nwant %#v", n, advisor, got, want)
			}
			if len(got) > 0 {
				got[0].Role = "changed child slot"
				got[0].Content = "changed child content"
				got[0].Parts = nil
				got[0].ToolCalls = nil
			}
			after, err := json.Marshal(messages)
			if err != nil || string(before) != string(after) {
				t.Fatalf("history %d advisor=%t changed parent", n, advisor)
			}
		}
	}
}

func BenchmarkSharedWorkerSeed(b *testing.B) {
	for _, advisor := range []bool{false, true} {
		for _, count := range []int{0, 1024} {
			a, _, _ := workerStartSeedDiagnosis(b, count, advisor)
			messages := a.ParentLoop.Messages
			for _, old := range []bool{true, false} {
				b.Run(fmt.Sprintf("advisor=%t/messages=%d/old=%t", advisor, count, old), func(b *testing.B) {
					want := referenceSharedWorkerSeed(messages, advisor)
					b.ReportAllocs()
					for b.Loop() {
						var got []llm.Message
						if old {
							got = referenceSharedWorkerSeed(messages, advisor)
						} else {
							got = sharedWorkerSeed(messages, advisor)
						}
						if len(got) != len(want) {
							b.Fatal("seed size changed")
						}
					}
				})
			}
		}
	}
}
