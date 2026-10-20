package agent

import (
	"encoding/json"
	"slices"
	"testing"

	"supercli/internal/tools/processsession"
)

// Keep the live benchmark grounded in the actual tool contract. A nonexistent
// action or mismatched process ID turns a model test into an impossible trace.
func TestShortSummaryFixtureUsesRealProcessActions(t *testing.T) {
	var schema struct {
		Properties map[string]struct{ Enum []string }
	}
	if err := json.Unmarshal([]byte((&processsession.Tool{}).Spec().Schema), &schema); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 6; round++ {
		for _, m := range shortSummaryRoundMessages(round) {
			for _, call := range m.ToolCalls {
				if call.Name != "process_session" {
					continue
				}
				var args struct{ Action, ID string }
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(schema.Properties["action"].Enum, args.Action) || args.ID != "proc-9000" {
					t.Fatalf("round %d has impossible process fixture: %+v", round, args)
				}
			}
		}
	}
}
