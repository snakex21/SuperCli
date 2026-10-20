package processsession

import (
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestProcessExecutionEvidenceRetainsConsumedDiagnostics(t *testing.T) {
	_, item, _ := waitFixture(t)
	item.startOrder = core.NextExecutionSequence()
	item.stdout.Write([]byte("testing: warning: no tests to run\n"))
	consumed := item.snapshot(maxBufferBytes)
	if consumed.Execution != nil {
		t.Fatal("running process supplied terminal check evidence")
	}
	item.stdout.Write([]byte("ok fixture/pkg 0.1s [no tests to run]\n"))
	finishWaitFixture(item, "done", 0)
	finished := item.snapshot(maxBufferBytes)
	if finished.Execution == nil || !finished.Execution.Completed || finished.Execution.ExitCode != 0 || !finished.Execution.OutputComplete || !strings.Contains(finished.Execution.Stdout, "testing: warning") {
		t.Fatalf("earlier poll hid verification evidence: %+v", finished.Execution)
	}
	if again := item.snapshot(maxBufferBytes); again.Execution != finished.Execution {
		t.Fatal("repeated terminal snapshot invented a new execution identity")
	}
	encoded, err := json.Marshal(finished)
	if err != nil || strings.Contains(string(encoded), `"Execution"`) || strings.Contains(string(encoded), `"StartedSequence"`) {
		t.Fatal("private executor metadata leaked into wire output")
	}
}

func TestProcessExecutionEvidenceMarksLostOutput(t *testing.T) {
	_, item, _ := waitFixture(t)
	item.startOrder = core.NextExecutionSequence()
	item.stdout.Write([]byte(strings.Repeat("x", maxBufferBytes+1)))
	finishWaitFixture(item, "done", 0)
	finished := item.snapshot(maxBufferBytes)
	if finished.Execution == nil || finished.Execution.OutputComplete {
		t.Fatal("ring-buffer loss was presented as complete check evidence")
	}
}
