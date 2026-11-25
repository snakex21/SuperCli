package llm

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Fault injection over tool-call histories (after 1.0.40). Every request
// built for Anthropic, OpenAI chat and OpenAI Responses must be valid per that
// provider's pairing rules, whatever the saved history looks like: compaction
// cuts, missing/duplicated/reordered parallel results, a provider switch with
// native reasoning, interrupted turns, a restart between a call and its
// result, worker reports saved mid-batch, empty/huge/image results, rewinds
// into the middle of a batch.

const faultModel = "pair-fixture"

// anthropicNative is an assistant's native content as Anthropic returned it:
// a signed thinking block plus its tool_use blocks.
func anthropicNative(calls []ToolCall) ContentPart {
	content := []map[string]any{{"type": "thinking", "thinking": "plan", "signature": "sig"}}
	for _, c := range calls {
		content = append(content, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": map[string]any{}})
	}
	raw, _ := json.Marshal(map[string]any{"type": "assistant", "content": content})
	return ContentPart{Type: PartTypeReasoning, Reasoning: &ReasoningBlock{Format: ReasoningAnthropic, Model: faultModel, Data: raw}}
}

func responsesReasoning(id string) ContentPart {
	raw, _ := json.Marshal(map[string]any{"type": "reasoning", "id": "rs_" + id, "summary": []any{}, "encrypted_content": "enc"})
	return ContentPart{Type: PartTypeReasoning, Reasoning: &ReasoningBlock{Format: ReasoningResponses, Model: faultModel, Data: raw}}
}

// faultHistory builds a random session and applies random faults.
func faultHistory(rng *rand.Rand) []Message {
	msgs := []Message{{Role: RoleSystem, Content: "system"}}
	turns := 1 + rng.Intn(6)
	for t := 0; t < turns; t++ {
		msgs = append(msgs, Message{Role: RoleUser, Content: fmt.Sprintf("request %d", t)})
		for b := 0; b < 1+rng.Intn(3); b++ {
			n := 1 + rng.Intn(5)
			calls := make([]ToolCall, n)
			for i := range calls {
				calls[i] = ToolCall{ID: fmt.Sprintf("toolu_%d_%d_%d", t, b, i), Name: []string{"read_lines", "task", "send_message"}[rng.Intn(3)], Arguments: `{"x":1}`}
			}
			a := Message{Role: RoleAssistant, ToolCalls: calls}
			switch rng.Intn(4) {
			case 0:
				a.Parts = []ContentPart{anthropicNative(calls)}
			case 1:
				a.Parts = []ContentPart{responsesReasoning(calls[0].ID)}
			case 2:
				a.Parts = []ContentPart{anthropicNative(calls), {Type: PartTypeText, Text: "checking"}}
			}
			msgs = append(msgs, a)
			order := rng.Perm(n)
			for _, i := range order {
				content := "ok " + calls[i].ID
				switch rng.Intn(12) {
				case 0:
					content = ""
				case 1:
					content = strings.Repeat("huge output line\n", 12000)
				}
				r := Message{Role: RoleTool, ToolCallID: calls[i].ID, Name: calls[i].Name, Content: content}
				if rng.Intn(15) == 0 {
					r.Parts = []ContentPart{{Type: PartTypeText, Text: "screenshot"}, {Type: PartTypeImage, Image: &ImageRef{MediaType: "image/png", Data: "iVBORw0KGgo="}}}
				}
				msgs = append(msgs, r)
			}
		}
		if rng.Intn(2) == 0 {
			msgs = append(msgs, Message{Role: RoleAssistant, Content: fmt.Sprintf("answer %d", t)})
		}
	}
	for f := 0; f < rng.Intn(5); f++ {
		msgs = applyFault(rng, msgs)
	}
	return msgs
}

func applyFault(rng *rand.Rand, msgs []Message) []Message {
	if len(msgs) < 2 {
		return msgs
	}
	i := 1 + rng.Intn(len(msgs)-1)
	switch rng.Intn(9) {
	case 0: // compaction cut at any index
		return append([]Message{msgs[0], {Role: RoleUser, Content: "Earlier context was compacted."}}, msgs[i:]...)
	case 1: // a result goes missing
		if msgs[i].Role == RoleTool {
			return append(msgs[:i:i], msgs[i+1:]...)
		}
	case 2: // a result is duplicated
		if msgs[i].Role == RoleTool {
			return append(msgs[:i+1:i+1], append([]Message{msgs[i]}, msgs[i+1:]...)...)
		}
	case 3: // two neighbours swap (results reordered, or a result before its call)
		if i+1 < len(msgs) {
			out := append([]Message(nil), msgs...)
			out[i], out[i+1] = out[i+1], out[i]
			return out
		}
	case 4: // a worker report saved in the middle of a batch
		report := Message{Role: RoleUser, Name: WorkerNotificationName, Content: "error: worker worker-7 (code), status=failed; call failed: http 429"}
		return append(msgs[:i:i], append([]Message{report}, msgs[i:]...)...)
	case 5: // interrupted turn: the user writes before the results exist
		out := append([]Message(nil), msgs[:i]...)
		out = append(out, Message{Role: RoleUser, Content: "continue"})
		for _, m := range msgs[i:] {
			if m.Role != RoleTool || rng.Intn(2) == 0 {
				out = append(out, m)
			}
		}
		return out
	case 6: // rewind into the middle of a batch
		return msgs[:i]
	case 7: // restart between a call and its result: the result is saved later
		if msgs[i].Role == RoleTool && i+2 < len(msgs) {
			r := msgs[i]
			out := append(append([]Message(nil), msgs[:i]...), msgs[i+1:i+3]...)
			return append(append(out, r), msgs[i+3:]...)
		}
	case 8: // provider switch: reasoning from another model on a later answer
		out := append([]Message(nil), msgs...)
		out = append(out, Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: &ReasoningBlock{Format: ReasoningResponses, Model: "other-model", Data: json.RawMessage(`{"type":"reasoning","id":"rs_x"}`)}}, {Type: PartTypeText, Text: "switched"}}})
		return out
	}
	return msgs
}

type wireMessage struct {
	Role      string            `json:"role"`
	Content   json.RawMessage   `json:"content"`
	ToolCalls []json.RawMessage `json:"tool_calls"`
	ToolID    string            `json:"tool_call_id"`
}

func blocksOf(raw json.RawMessage) []map[string]any {
	var blocks []map[string]any
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// validAnthropic: every assistant tool_use is answered by tool_result blocks
// at the start of the user turn that immediately follows; every tool_result
// answers the preceding assistant; no id twice; no empty content.
func validAnthropic(body []byte) error {
	var req struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	seen := map[string]bool{}
	var pending map[string]bool
	for i, m := range req.Messages {
		blocks := blocksOf(m.Content)
		if len(blocks) == 0 {
			var text string
			if json.Unmarshal(m.Content, &text) != nil || text == "" {
				return fmt.Errorf("messages.%d: empty content", i)
			}
		}
		if m.Role == "assistant" {
			if len(pending) > 0 {
				return fmt.Errorf("messages.%d: tool_use without tool_result before the next assistant turn", i)
			}
			pending = map[string]bool{}
			for _, b := range blocks {
				if b["type"] == "tool_use" {
					id, _ := b["id"].(string)
					if seen[id] {
						return fmt.Errorf("messages.%d: duplicate tool_use id %s", i, id)
					}
					seen[id] = true
					pending[id] = true
				}
			}
			continue
		}
		// user turn: tool_results first, only for pending ids
		other := false
		for _, b := range blocks {
			if b["type"] != "tool_result" {
				other = true
				continue
			}
			id, _ := b["tool_use_id"].(string)
			if !pending[id] {
				return fmt.Errorf("messages.%d: tool_result %s without a preceding tool_use", i, id)
			}
			if other {
				return fmt.Errorf("messages.%d: tool_result after other content", i)
			}
			delete(pending, id)
		}
		if other && len(pending) > 0 {
			return fmt.Errorf("messages.%d: tool_use ids were found without tool_result blocks immediately after", i)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("unanswered tool_use at the end")
	}
	return nil
}

// validChat: assistant tool_calls are followed by exactly their tool messages.
func validChat(body []byte) error {
	var req struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	var pending map[string]bool
	for i, m := range req.Messages {
		if m.Role == "tool" {
			if !pending[m.ToolID] {
				return fmt.Errorf("messages.%d: tool result %s without its call", i, m.ToolID)
			}
			delete(pending, m.ToolID)
			continue
		}
		if len(pending) > 0 {
			return fmt.Errorf("messages.%d: calls without results before a %s message", i, m.Role)
		}
		pending = map[string]bool{}
		for _, raw := range m.ToolCalls {
			var c struct{ ID string }
			_ = json.Unmarshal(raw, &c)
			pending[c.ID] = true
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("calls without results at the end")
	}
	return nil
}

// validResponses: each function_call has exactly one later output; each
// output follows its call.
func validResponses(body []byte) error {
	var req struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	calls, outputs := map[string]int{}, map[string]int{}
	for i, it := range req.Input {
		id, _ := it["call_id"].(string)
		switch it["type"] {
		case "function_call":
			calls[id]++
			if calls[id] > 1 {
				return fmt.Errorf("input.%d: duplicate call %s", i, id)
			}
		case "function_call_output":
			if calls[id] == 0 {
				return fmt.Errorf("input.%d: output %s before or without its call", i, id)
			}
			outputs[id]++
		}
	}
	for id := range calls {
		if outputs[id] != 1 {
			return fmt.Errorf("call %s has %d outputs", id, outputs[id])
		}
	}
	return nil
}

func validateAllProviders(t *testing.T, label string, msgs []Message) {
	t.Helper()
	validators := map[string]func([]byte) error{"anthropic": validAnthropic, "chat": validChat, "responses": validResponses}
	for name, build := range pairingRequestBuilders() {
		body, err := build(msgs)
		if err != nil {
			t.Fatalf("%s %s: build: %v", label, name, err)
		}
		if err := validators[name](body); err != nil {
			raw, _ := json.Marshal(msgs)
			t.Fatalf("%s %s: invalid request: %v\nhistory: %.2000s", label, name, err, raw)
		}
	}
}

func TestToolPairingFaultInjection(t *testing.T) {
	rng := rand.New(rand.NewSource(20261125))
	runs := 3000
	if testing.Short() {
		runs = 300
	}
	for i := 0; i < runs; i++ {
		validateAllProviders(t, fmt.Sprintf("history %d", i), faultHistory(rng))
	}
}

// Every compaction cut index of one long history.
func TestToolPairingEveryCompactionCut(t *testing.T) {
	msgs := faultHistory(rand.New(rand.NewSource(7)))
	for len(msgs) < 30 {
		msgs = append(msgs, faultHistory(rand.New(rand.NewSource(int64(len(msgs)))))[1:]...)
	}
	for cut := 1; cut < len(msgs); cut++ {
		cutMsgs := append([]Message{msgs[0], {Role: RoleUser, Content: "Earlier context was compacted."}}, msgs[cut:]...)
		validateAllProviders(t, fmt.Sprintf("cut %d", cut), cutMsgs)
	}
}

// The reported failure (anonymized): 7 parallel task/send_message calls with
// native Anthropic thinking; a background worker report was saved between the
// calls and their results. Anthropic answered "tool_use ids were found
// without tool_result blocks immediately after". The request must now be
// valid and keep all 7 calls with their results.
func TestToolPairingRegressionWorkerReportMidBatch(t *testing.T) {
	calls := make([]ToolCall, 7)
	for i := range calls {
		calls[i] = ToolCall{ID: fmt.Sprintf("toolu_reg_%d", i), Name: []string{"send_message", "task"}[i%2], Arguments: `{"to":"worker-1"}`}
	}
	msgs := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: "Earlier context was compacted. Continue using this summary: ..."},
		{Role: RoleUser, Name: WorkerNotificationName, Content: "error: worker worker-1 (code), status=failed; call failed: http 429: usage limit"},
		{Role: RoleAssistant, ToolCalls: calls, Parts: []ContentPart{anthropicNative(calls)}},
		{Role: RoleUser, Name: WorkerNotificationName, Content: "error: worker worker-2 (code), status=failed; call failed: http 429: usage limit"},
	}
	for i := len(calls) - 1; i >= 0; i-- {
		msgs = append(msgs, Message{Role: RoleTool, ToolCallID: calls[i].ID, Name: calls[i].Name, Content: "<task-notification>running</task-notification>"})
	}
	msgs = append(msgs,
		Message{Role: RoleAssistant, Content: "Workers resumed."},
		Message{Role: RoleSystem, Content: "[conversation rewind] earlier messages removed"},
		Message{Role: RoleUser, Content: "can you resume the other agents?"},
	)
	validateAllProviders(t, "regression", msgs)
	body, _ := pairingRequestBuilders()["anthropic"](msgs)
	for _, c := range calls {
		if strings.Count(string(body), c.ID) != 2 {
			t.Fatalf("call %s lost its tool_use or tool_result: %d occurrences", c.ID, strings.Count(string(body), c.ID))
		}
	}
	if !strings.Contains(string(body), `"signature":"sig"`) {
		t.Fatal("the native thinking block of the repaired batch was dropped")
	}
}
