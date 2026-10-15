package llm

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestResponsesRetainsToolIdentityAcrossPartialAndRepeatedEvents(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"item-a","type":"function_call","call_id":"call-a","name":"task","arguments":""}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"id":"item-b","type":"function_call","call_id":"call-b","name":"read_lines","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"item-a","output_index":0,"delta":"{\"prompt\":\"review\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"item-b","output_index":2,"arguments":"{\"file\":\"a.go\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"item-a","type":"function_call","arguments":"{\"prompt\":\"review\"}"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"item-a","type":"function_call","arguments":"{\"prompt\":\"review\"}"}}`,
		`{"type":"response.completed","response":{"output":[{"id":"item-a","type":"function_call","call_id":"call-a","name":"task","arguments":"{\"prompt\":\"review\"}"}]}}`,
	}, "\n\ndata: ")
	out := make(chan Delta, 16)
	(&CodexProvider{}).streamCodexSSE(context.Background(), strings.NewReader("data: "+stream+"\n\n"), out)
	close(out)
	var calls []ToolCall
	for delta := range out {
		if delta.Err != nil {
			t.Fatal(delta.Err)
		}
		if delta.ToolCall != nil {
			calls = append(calls, *delta.ToolCall)
		}
	}
	want := []ToolCall{{ID: "call-a", Name: "task", Arguments: `{"prompt":"review"}`}, {ID: "call-b", Name: "read_lines", Arguments: `{"file":"a.go"}`}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%+v want %+v", calls, want)
	}
}

func TestResponsesCompletedOnlyToolAndConflictingIdentity(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		first := ""
		if conflict {
			first = `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"delete_file","call_id":"a"}}` + "\n\n"
		}
		stream := first + `data: {"type":"response.completed","response":{"output":[{"type":"function_call","name":"read_lines","call_id":"a","arguments":"{}"}]}}` + "\n\n"
		out := make(chan Delta, 8)
		(&CodexProvider{}).streamCodexSSE(context.Background(), strings.NewReader(stream), out)
		close(out)
		calls, failures := 0, 0
		for delta := range out {
			if delta.ToolCall != nil {
				calls++
			}
			if delta.Err != nil {
				failures++
			}
		}
		if conflict && (calls != 0 || failures != 1) {
			t.Fatal("conflicting names were executed or hidden")
		}
		if !conflict && (calls != 1 || failures != 0) {
			t.Fatal("completed-only response lost its tool")
		}
	}
}

func TestResponsesRejectsAmbiguousOrRewrittenParallelCallsBeforeEmission(t *testing.T) {
	const first = `{"type":"response.output_item.added","output_index":0,"item":{"id":"item-a","type":"function_call","name":"task","call_id":"call-a"}}`
	for _, test := range []struct {
		name   string
		frames []string
	}{
		{"changed item", []string{first, `{"type":"response.output_item.done","output_index":0,"item":{"id":"item-b","type":"function_call","name":"task","call_id":"call-a","arguments":"{}"}}`}},
		{"changed call", []string{first, `{"type":"response.output_item.done","output_index":0,"item":{"id":"item-a","type":"function_call","name":"task","call_id":"call-b","arguments":"{}"}}`}},
		{"changed index", []string{first, `{"type":"response.output_item.done","output_index":1,"item":{"id":"item-a","type":"function_call","name":"task","call_id":"call-a","arguments":"{}"}}`}},
		{"contradictory item fields", []string{first, `{"type":"response.output_item.done","item_id":"item-a","item":{"id":"item-b","type":"function_call","arguments":"{}"}}`}},
		{"identity points at two calls", []string{first, `{"type":"response.output_item.added","output_index":1,"item":{"id":"item-b","type":"function_call","name":"task","call_id":"call-b"}}`, `{"type":"response.output_item.done","item":{"id":"item-a","type":"function_call","call_id":"call-b","arguments":"{}"}}`}},
		{"anonymous parallel arguments", []string{first, `{"type":"response.output_item.added","output_index":1,"item":{"id":"item-b","type":"function_call","name":"task","call_id":"call-b"}}`, `{"type":"response.function_call_arguments.delta","delta":"{}"}`}},
		{"anonymous added items", []string{`{"type":"response.output_item.added","item":{"type":"function_call","name":"task"}}`, `{"type":"response.output_item.added","item":{"type":"function_call","name":"task"}}`}},
		{"mixed anonymous parallel item", []string{first, `{"type":"response.output_item.added","item":{"type":"function_call","name":"task"}}`}},
		{"changed final arguments", []string{first, `{"type":"response.function_call_arguments.done","item_id":"item-a","arguments":"{\"prompt\":\"review\"}"}`, `{"type":"response.output_item.done","item":{"id":"item-a","type":"function_call","arguments":"{\"prompt\":\"delete\"}"}}`}},
		{"large integer identity rewritten", []string{first, `{"type":"response.function_call_arguments.done","item_id":"item-a","arguments":"{\"id\":9007199254740992}"}`, `{"type":"response.output_item.done","item":{"id":"item-a","type":"function_call","arguments":"{\"id\":9007199254740993}"}}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := append(append([]string(nil), test.frames...), `{"type":"response.completed"}`)
			calls, failures := collectResponsesToolFrames(frames)
			if len(calls) != 0 || len(failures) != 1 {
				t.Fatalf("ambiguous calls were emitted or error lost: calls=%+v errors=%v", calls, failures)
			}
		})
	}
}

func TestResponsesToolIdentityNamespacesAreSeparate(t *testing.T) {
	calls, failures := collectResponsesToolFrames([]string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"item-a","type":"function_call","name":"task","call_id":"shared"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"shared","type":"function_call","name":"read_lines","call_id":"call-b"}}`,
		`{"type":"response.function_call_arguments.done","item_id":"item-a","arguments":"{\"prompt\":\"review\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"shared","arguments":"{\"file\":\"a.go\"}"}`,
		`{"type":"response.completed"}`,
	})
	want := []ToolCall{{ID: "shared", Name: "task", Arguments: `{"prompt":"review"}`}, {ID: "call-b", Name: "read_lines", Arguments: `{"file":"a.go"}`}}
	if len(failures) > 0 || !reflect.DeepEqual(calls, want) {
		t.Fatalf("item_id collided with call_id: calls=%+v errors=%v", calls, failures)
	}
}

func TestResponsesPartialSeedAndEquivalentTerminalArguments(t *testing.T) {
	for _, test := range []struct {
		name, want string
		frames     []string
	}{
		{"seed plus delta", `{"prompt":"review"}`, []string{
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"task","call_id":"call-a","arguments":"{\"prompt\":"}}`,
			`{"type":"response.function_call_arguments.delta","delta":"\"review\"}"}`,
		}},
		{"late identity on anonymous item", `{"prompt":"review"}`, []string{
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"task"}}`,
			`{"type":"response.function_call_arguments.delta","delta":"{\"prompt\":\"review\"}"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"item-a","type":"function_call","name":"task","call_id":"call-a","arguments":"{\"prompt\":\"review\"}"}}`,
		}},
		{"unkeyed repeated terminal", `{"a":1,"b":2}`, []string{
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"task","call_id":"call-a"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","arguments":"{\"b\":2, \"a\":1}"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","arguments":"{\"a\":1,\"b\":2}"}}`,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, failures := collectResponsesToolFrames(append(test.frames, `{"type":"response.completed"}`))
			if len(failures) > 0 || len(calls) != 1 || calls[0].ID != "call-a" || calls[0].Arguments != test.want {
				t.Fatalf("partial/repeated tool lost: %+v errors=%v", calls, failures)
			}
		})
	}
}

func collectResponsesToolFrames(frames []string) ([]ToolCall, []error) {
	out := make(chan Delta, len(frames)*2+8)
	(&CodexProvider{}).streamCodexSSE(context.Background(), strings.NewReader("data: "+strings.Join(frames, "\n\ndata: ")+"\n\n"), out)
	close(out)
	var calls []ToolCall
	var failures []error
	for delta := range out {
		if delta.ToolCall != nil {
			calls = append(calls, *delta.ToolCall)
		}
		if delta.Err != nil {
			failures = append(failures, delta.Err)
		}
	}
	return calls, failures
}
