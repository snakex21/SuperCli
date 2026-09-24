package llm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func pairingFixture(batches, width int) []Message {
	messages := []Message{{Role: RoleSystem, Content: "Fixture system"}, {Role: RoleUser, Content: "Inspect the project"}}
	for batch := 0; batch < batches; batch++ {
		calls := make([]ToolCall, width)
		for i := range calls {
			calls[i] = ToolCall{ID: fmt.Sprintf("call_%d_%d", batch, i), Name: "read_lines", Arguments: "{\"path\":\"src/module.go\",\"from\":1,\"to\":30}"}
		}
		messages = append(messages, Message{Role: RoleAssistant, ToolCalls: calls})
		// Results can legitimately complete in an order different from calls.
		for i := len(calls) - 1; i >= 0; i-- {
			messages = append(messages, Message{Role: RoleTool, ToolCallID: calls[i].ID, Content: strings.Repeat("line of source\n", 40)})
		}
	}
	return append(messages, Message{Role: RoleAssistant, Content: "Finished"})
}

func TestToolPairCheckCompatibility(t *testing.T) {
	compare := func(label string, messages []Message) {
		t.Helper()
		before, err := json.Marshal(messages)
		if err != nil {
			t.Fatal(err)
		}
		want := legacyToolCallHistoryNeedsRepair(messages)
		if got := toolCallHistoryNeedsRepair(messages); got != want {
			t.Fatalf("%s: needs repair=%v, want %v; messages=%s", label, got, want, before)
		}
		after, _ := json.Marshal(messages)
		if string(before) != string(after) {
			t.Fatalf("%s: validation mutated history", label)
		}
	}
	compare("nil", nil)
	for _, width := range []int{1, 2, 8, 32} {
		base := pairingFixture(3, width)
		compare(fmt.Sprintf("healthy/%d", width), base)
		if toolCallHistoryNeedsRepair(base) {
			t.Fatal("valid reordered results require repair")
		}
		for i := range base {
			// Resume/compaction/hidden ranges can cut through any protocol block.
			compare(fmt.Sprintf("prefix/%d/%d", width, i), base[:i])
			compare(fmt.Sprintf("suffix/%d/%d", width, i), base[i:])
			deleted := append([]Message(nil), base[:i]...)
			deleted = append(deleted, base[i+1:]...)
			compare("deleted message", deleted)
			for _, role := range []Role{RoleUser, RoleSystem, RoleTool, RoleAssistant} {
				changed := append([]Message(nil), base...)
				changed[i].Role = role
				compare("wrong role", changed)
			}
			for _, id := range []string{"", "unknown", "call_0_0", "call_2_0"} {
				changed := append([]Message(nil), base...)
				if len(changed[i].ToolCalls) > 0 {
					changed[i].ToolCalls = append([]ToolCall(nil), changed[i].ToolCalls...)
					changed[i].ToolCalls[0].ID = id
				} else {
					changed[i].ToolCallID = id
				}
				compare("reused or missing id", changed)
			}
		}
	}
	// Histories malformed in several ways at once, with a fixed seed.
	rng := rand.New(rand.NewSource(421))
	for attempt := 0; attempt < 1000; attempt++ {
		messages := pairingFixture(1+rng.Intn(5), 1+rng.Intn(12))
		for edit := 0; edit < 1+rng.Intn(8); edit++ {
			i := rng.Intn(len(messages))
			switch rng.Intn(4) {
			case 0:
				j := rng.Intn(len(messages))
				messages[i], messages[j] = messages[j], messages[i]
			case 1:
				messages[i].ToolCallID = fmt.Sprintf("call_%d_%d", rng.Intn(4), rng.Intn(12))
			case 2:
				messages[i].ToolCalls = []ToolCall{{ID: "repeated", Name: "read_lines", Arguments: "{}"}}
			case 3:
				messages[i].Role = []Role{RoleSystem, RoleUser, RoleTool, RoleAssistant}[rng.Intn(4)]
			}
		}
		compare(fmt.Sprint(attempt), messages)
	}
}

func TestToolPairCheckRejectsAlreadyConsumedResult(t *testing.T) {
	messages := []Message{call("old", "read"), result("old"), call("new", "read"), result("old")}
	if !toolCallHistoryNeedsRepair(messages) {
		t.Fatal("a result from a completed batch satisfied a different pending call")
	}
	messages[3] = result("new")
	if toolCallHistoryNeedsRepair(messages) {
		t.Fatal("valid sequence rejected")
	}
	// Reusing an ID is invalid even after its original result was consumed.
	messages[2] = call("old", "read")
	messages[3] = result("old")
	if !toolCallHistoryNeedsRepair(messages) {
		t.Fatal("duplicate completed ID accepted")
	}
}

func TestToolPairCheckProviderRequestsUnchanged(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		messages := pairingFixture(3, 8)
		if malformed {
			messages[2].ToolCalls[0].ID = messages[2].ToolCalls[1].ID
		}
		// An already repaired projection and the original history must encode
		// to the same body at all three transport boundaries.
		repaired := repairToolCallIDs(messages)
		for name, build := range pairingRequestBuilders() {
			original, err := build(messages)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			t.Logf("wire %s malformed=%v bytes=%d sha256=%x", name, malformed, len(original), sha256.Sum256(original))
			again, err := build(repaired)
			if err != nil || !reflect.DeepEqual(original, again) {
				t.Fatalf("%s malformed=%v: non-idempotent request, err=%v", name, malformed, err)
			}
		}
	}
}

func pairingRequestBuilders() map[string]func([]Message) ([]byte, error) {
	return map[string]func([]Message) ([]byte, error){
		"chat": func(messages []Message) ([]byte, error) {
			return buildOpenAIRequestWithReasoning("pair-fixture", messages, nil, false, false, openAIReasoningNone)
		},
		"responses": func(messages []Message) ([]byte, error) {
			return buildCodexRequestWithEffort("pair-fixture", messages, nil, false, "")
		},
		"anthropic": func(messages []Message) ([]byte, error) {
			return buildAnthropicRequest("pair-fixture", messages, nil, false, 4096)
		},
	}
}

var pairingBoolSink bool
var pairingBodySink []byte

func BenchmarkToolPairCheck(b *testing.B) {
	for _, shape := range [][2]int{{0, 0}, {1, 1}, {32, 1}, {32, 8}, {32, 32}, {128, 8}} {
		b.Run(fmt.Sprintf("%dx%d", shape[0], shape[1]), func(b *testing.B) {
			messages := pairingFixture(shape[0], shape[1])
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pairingBoolSink = toolCallHistoryNeedsRepair(messages)
			}
			if pairingBoolSink {
				b.Fatal("fixture was considered malformed")
			}
		})
	}
}

func BenchmarkToolPairRequest(b *testing.B) {
	for _, name := range []string{"chat", "responses", "anthropic"} {
		b.Run(name, func(b *testing.B) {
			messages := pairingFixture(32, 8)
			build := pairingRequestBuilders()[name]
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				body, err := build(messages)
				if err != nil {
					b.Fatal(err)
				}
				pairingBodySink = body
			}
		})
	}
}

// Former validator retained as a compatibility oracle.
func legacyToolCallHistoryNeedsRepair(msgs []Message) bool {
	seen := make(map[string]bool)
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]

		if m.Role != RoleAssistant && len(m.ToolCalls) > 0 {
			return true
		}
		if m.Role != RoleTool && m.ToolCallID != "" {
			return true
		}
		if m.Role == RoleTool {
			return true // not consumed as part of a preceding valid batch
		}
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}

		need := make(map[string]int, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || seen[tc.ID] {
				return true
			}
			seen[tc.ID] = true
			need[tc.ID]++
		}

		j := i + 1
		got := 0
		for j < len(msgs) && msgs[j].Role == RoleTool {
			r := msgs[j]
			if r.ToolCallID == "" || len(r.ToolCalls) > 0 || need[r.ToolCallID] == 0 {
				return true
			}
			need[r.ToolCallID]--
			got++
			j++
		}
		if got != len(m.ToolCalls) {
			return true
		}
		for _, n := range need {
			if n != 0 {
				return true
			}
		}
		i = j - 1
	}
	return false
}
