package agent

import (
	"strings"
	"testing"
)

func TestProcessPruningRetainsIncompleteCapture(t *testing.T) {
	for _, body := range []string{
		`{"id":"proc-1","status":"done","exit_code":0,"output_incomplete":true}`,
		`{"id":"proc-2","status":"failed","exit_code":7,"output_incomplete":true}`,
		"error: process_session proc-3 output_incomplete=true: command_failed exit=7: exit status 7\nOutput capture incomplete",
	} {
		got := processStatusForPrune(body, "")
		if !strings.Contains(got, "output_incomplete=true") {
			t.Fatalf("capture warning lost: %s", got)
		}
	}
}
