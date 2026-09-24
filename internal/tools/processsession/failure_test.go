package processsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFailedProcessIsToolFailure(t *testing.T) {
	for _, status := range []string{"failed", "timeout", "done"} {
		t.Run(status, func(t *testing.T) {
			tool := New(t.TempDir())
			// A completed snapshot, no process to poll or wall-clock wait required.
			item := &process{id: "proc-1", status: status, exitCode: 7, command: []string{"test-runner"}, started: time.Now(), ended: time.Now(), stdout: newStreamBuffer(maxBufferBytes), stderr: newStreamBuffer(maxBufferBytes)}
			_, _ = item.stderr.Write([]byte("specific failing assertion"))
			tool.Manager.items[item.id] = item
			result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"poll","id":"proc-1"}`))
			if err != nil {
				t.Fatal(err)
			}
			if result.Err == nil {
				t.Fatalf("failed process reported success: %s", result.Text)
			}
			if !strings.Contains(result.ModelContent(), "specific failing assertion") || !strings.Contains(result.ModelContent(), "command_failed") {
				t.Fatalf("missing actionable diagnostic: %s", result.ModelContent())
			}
			var saved snapshot
			if err := json.Unmarshal([]byte(result.Text), &saved); err != nil || saved.Status != status {
				t.Fatalf("UI lost snapshot: %s %v", result.Text, err)
			}
		})
	}
}

func TestProcessListingAndIntentionalStopAreNotCommandFailures(t *testing.T) {
	tool := New(t.TempDir())
	done := make(chan struct{})
	close(done)
	item := &process{id: "proc-1", status: "stopped", exitCode: 1, done: done, started: time.Now(), ended: time.Now(), stdout: newStreamBuffer(maxBufferBytes), stderr: newStreamBuffer(maxBufferBytes)}
	tool.Manager.items[item.id] = item
	for _, raw := range []string{`{"action":"poll","id":"proc-1"}`, `{"action":"list"}`} {
		result, err := tool.Execute(context.Background(), json.RawMessage(raw))
		if err != nil || result.Err != nil {
			t.Fatalf("management action failed: %v %v", err, result.Err)
		}
	}
}

func TestProcessFailedExitReachesModel(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	raw, _ := json.Marshal(map[string]any{"action": "start", "command": helperCommand("fail"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	start, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var initial snapshot
	if err := json.Unmarshal([]byte(start.Text), &initial); err != nil {
		t.Fatal(err)
	}
	item, err := tool.Manager.get(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-item.done:
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit")
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"poll","id":"`+initial.ID+`"}`))
	if err != nil || result.Err == nil {
		t.Fatalf("failed command reported success: %+v %v", result, err)
	}
	if !strings.Contains(result.ModelContent(), "exit=7") {
		t.Fatalf("missing exit code: %s", result.ModelContent())
	}
	// Output may have been consumed by start if the helper finished immediately.
	if !strings.Contains(start.Text+result.Text, "specific failing assertion") {
		t.Fatal("diagnostic lost")
	}
}
