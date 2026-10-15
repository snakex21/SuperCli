package llm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type responsesToolCall struct {
	id, itemID, name, seed string
	index                  *int
	arguments              strings.Builder
	full                   *string
}

// Responses gateways may put the identity in output_item.added, the arguments
// in deltas and only a partial item in output_item.done. Keep one record per
// output item until the terminal event; repeated done events never execute twice.
type responsesToolStream struct {
	byItemID map[string]*responsesToolCall
	byCallID map[string]*responsesToolCall
	byIndex  map[int]*responsesToolCall
	ordered  []*responsesToolCall
}

func (s *responsesToolStream) add(ev codexEvent) error {
	itemID, callID, name := ev.ItemID, "", ev.Name
	if ev.Item != nil {
		if ev.Item.ID != "" {
			if itemID != "" && itemID != ev.Item.ID {
				return fmt.Errorf("conflicting response tool item IDs")
			}
			itemID = ev.Item.ID
		}
		callID = ev.Item.CallID
		if ev.Item.Name != "" {
			name = ev.Item.Name
		}
	}
	if ev.OutputIndex != nil && *ev.OutputIndex < 0 {
		return fmt.Errorf("invalid response tool output index")
	}
	if s.byItemID == nil {
		s.byItemID = make(map[string]*responsesToolCall)
		s.byCallID = make(map[string]*responsesToolCall)
		s.byIndex = make(map[int]*responsesToolCall)
	}
	var call *responsesToolCall
	// Item IDs, call IDs and output positions are separate namespaces. Every
	// supplied identity must resolve to the same record, not just the first one.
	for _, candidate := range []*responsesToolCall{s.byItemID[itemID], s.byCallID[callID]} {
		if candidate != nil {
			if call != nil && candidate != call {
				return fmt.Errorf("conflicting response tool identity")
			}
			call = candidate
		}
	}
	if ev.OutputIndex != nil {
		if indexed := s.byIndex[*ev.OutputIndex]; indexed != nil {
			if call != nil && indexed != call {
				return fmt.Errorf("conflicting response tool index")
			}
			call = indexed
		}
	}
	if call == nil && itemID == "" && callID == "" && ev.OutputIndex == nil &&
		(ev.Type == "response.function_call_arguments.delta" || ev.Type == "response.function_call_arguments.done" ||
			(ev.Type == "response.output_item.done" && len(s.ordered) > 0)) {
		// Some compatible gateways omit identifiers in a single-call stream.
		// Only that unambiguous case may reuse a record; never guess among
		// parallel calls and attach arguments to the wrong action.
		if len(s.ordered) != 1 {
			return fmt.Errorf("response tool arguments have no unambiguous item identity")
		}
		call = s.ordered[0]
	}
	if call == nil && len(s.ordered) > 0 {
		for _, previous := range s.ordered {
			if previous.id != "" || previous.itemID != "" || previous.index != nil {
				continue
			}
			// A gateway can identify its sole anonymous added item only at the
			// done event. Bind it there instead of executing it a second time.
			// Mixed or parallel anonymous items cannot be correlated safely.
			if len(s.ordered) != 1 || ev.Type == "response.output_item.added" {
				return fmt.Errorf("response tool calls have no unambiguous item identity")
			}
			call = previous
		}
		if call == nil && itemID == "" && callID == "" && ev.OutputIndex == nil {
			return fmt.Errorf("parallel response tool call has no item identity")
		}
	}
	if call == nil {
		call = &responsesToolCall{}
		s.ordered = append(s.ordered, call)
	}
	if itemID != "" && call.itemID != "" && itemID != call.itemID {
		return fmt.Errorf("conflicting response tool item ID")
	}
	if ev.OutputIndex != nil {
		if call.index != nil && *call.index != *ev.OutputIndex {
			return fmt.Errorf("conflicting response tool index")
		}
		index := *ev.OutputIndex
		call.index = &index
		s.byIndex[*ev.OutputIndex] = call
	}
	if itemID != "" {
		call.itemID = itemID
		s.byItemID[itemID] = call
	}
	if callID != "" {
		if call.id != "" && call.id != callID {
			return fmt.Errorf("conflicting response tool call ID")
		}
		call.id = callID
		s.byCallID[callID] = call
	}
	if name != "" {
		if call.name != "" && call.name != name {
			return fmt.Errorf("conflicting response tool function names")
		}
		call.name = name
	}
	if ev.Type == "response.function_call_arguments.delta" {
		call.arguments.WriteString(ev.Delta)
	}
	if ev.Item != nil && ev.Item.Arguments != "" {
		if ev.Type == "response.output_item.added" {
			call.seed = ev.Item.Arguments
		} else {
			args := ev.Item.Arguments
			if err := call.setFull(args); err != nil {
				return err
			}
		}
	}
	if ev.Arguments != nil {
		args := *ev.Arguments
		if err := call.setFull(args); err != nil {
			return err
		}
	}
	return nil
}

func (c *responsesToolCall) setFull(args string) error {
	if c.full != nil && *c.full != args && !responsesArgumentsEqual(*c.full, args) {
		return fmt.Errorf("conflicting response tool arguments")
	}
	c.full = &args
	return nil
}

func responsesArgumentsEqual(a, b string) bool {
	decode := func(raw string) (any, error) {
		if strings.TrimSpace(raw) == "" {
			raw = "{}"
		}
		if !json.Valid([]byte(raw)) {
			return nil, fmt.Errorf("invalid JSON arguments")
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	left, leftErr := decode(a)
	right, rightErr := decode(b)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
}

func (s *responsesToolStream) calls() []ToolCall {
	var calls []ToolCall
	for _, call := range s.ordered {
		args := call.seed
		if call.arguments.Len() > 0 {
			args += call.arguments.String()
		}
		if call.full != nil {
			args = *call.full
		}
		calls = append(calls, ToolCall{ID: call.id, Name: call.name, Arguments: args})
	}
	return calls
}
