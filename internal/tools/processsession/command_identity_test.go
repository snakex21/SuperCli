package processsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools/core"
)

func TestProcessCommandKeySurvivesWaitWithoutExposingEnvironment(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	reg := core.NewRegistry()
	reg.MustRegister(tool.Spec())
	env := []string{"SUPERCLI_PROCESS_HELPER=1", "SUPERCLI_PRIVATE_FIXTURE=private-test-value"}
	command := helperCommand("echo")
	args, _ := json.Marshal(map[string]any{"action": "start", "command": command, "env": env, "yield_ms": 0})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start, err := reg.Execute(ctx, "process_session", args)
	if err != nil || start.Err != nil {
		t.Fatalf("start failed: %v %+v", err, start)
	}
	var state snapshot
	if err := json.Unmarshal([]byte(start.Text), &state); err != nil {
		t.Fatal(err)
	}
	waitArgs, _ := json.Marshal(map[string]string{"action": "wait", "id": state.ID})
	finished, err := reg.Execute(ctx, "process_session", waitArgs)
	if err != nil || finished.Err != nil {
		t.Fatalf("wait failed: %v %+v", err, finished)
	}
	want := core.CommandKey(command, tool.BaseDir, env)
	for _, result := range []core.Result{start, finished} {
		if result.CommandKey == nil || *result.CommandKey != want {
			t.Fatal("process result lost the start environment identity")
		}
		ui, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{result.Text, string(ui), reg.ModelResultContent("process_session", result)} {
			if strings.Contains(text, "\"CommandKey\"") || strings.Contains(text, "SUPERCLI_PRIVATE_FIXTURE") || strings.Contains(text, "private-test-value") {
				t.Fatal("process environment metadata leaked into output")
			}
		}
	}
}
