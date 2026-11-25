package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Which coordinator commands go through the background fallback, and with
// which timeout (the runaway limit unless a shorter one was asked).
func TestCoordinatorCommandsUseTheBackgroundFallback(t *testing.T) {
	timeout := func(raw json.RawMessage) int64 {
		var a struct {
			TimeoutMS int64 `json:"timeout_ms"`
		}
		_ = json.Unmarshal(raw, &a)
		return a.TimeoutMS
	}
	orch := &Loop{route: RouteCoordinator, orchestrator: true}
	for _, tc := range []struct {
		args string
		bg   bool
		want int64
	}{
		{`{"command":["gradlew","build"],"timeout_ms":3600000}`, true, 3600000},
		{`{"command":["gradlew","build"]}`, true, coordinatorRunawayMS},
		{`{"command":["gradlew","build"],"timeout_ms":99999999}`, true, coordinatorRunawayMS},
		{`{"command":["gradlew","build"],"timeout_ms":30000}`, false, 30000},
	} {
		got, bg := orch.capCoordinatorCommand("ctx_execute", json.RawMessage(tc.args))
		if bg != tc.bg || timeout(got) != tc.want {
			t.Fatalf("%s -> background=%v timeout=%d, want %v/%d", tc.args, bg, timeout(got), tc.bg, tc.want)
		}
	}
	withWorkers := &Loop{route: RouteCoordinator}
	withWorkers.backgroundActive.Store(2)
	if _, bg := withWorkers.capCoordinatorCommand("ctx_execute", json.RawMessage(`{"command":["x"],"timeout_ms":600000}`)); !bg {
		t.Fatal("a coordinator with running workers keeps long commands in the foreground")
	}
	plain := &Loop{route: RouteCoordinator}
	if _, bg := plain.capCoordinatorCommand("ctx_execute", json.RawMessage(`{"command":["x"],"timeout_ms":600000}`)); bg {
		t.Fatal("a plain coordinator's (or a worker's) command was changed")
	}
}

// A long build is not killed: it continues in the background, the turn ends,
// a worker report arrives meanwhile, and the completion notification comes
// once, with its exit code.
func TestLongCoordinatorBuildContinuesInTheBackground(t *testing.T) {
	saved := coordinatorForeground
	coordinatorForeground = 150 * time.Millisecond
	defer func() { coordinatorForeground = saved }()
	release := make(chan struct{})
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "run", Schema: `{"type":"object"}`,
		Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			select {
			case <-release:
			case <-ctx.Done():
				return tools.Result{Err: ctx.Err()}, ctx.Err()
			}
			return tools.Result{Text: `{"stdout":"BUILD SUCCESSFUL","exit_code":0}`}, nil
		}})
	reg.MarkAlwaysOn("ctx_execute")
	p := &stubProvider{name: "coord", scripts: [][]llm.Delta{
		callStep(tcall("b1", "ctx_execute", `{"command":["gradlew","build"],"timeout_ms":3600000}`)),
		say("The build runs in the background."),
	}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	l.orchestrator = true
	begin := time.Now()
	answer := runTurnText(t, l, "zbuduj projekt")
	if took := time.Since(begin); took > 5*time.Second || !strings.Contains(answer, "background") {
		t.Fatalf("the turn waited for the build (%s): %q", took, answer)
	}
	if !requestContains(p.reqs[1], "<background-command>") {
		t.Fatal("the model did not get the background handle")
	}
	// A worker report while the build runs is delivered at once.
	l.DeliverWorkerReport(context.Background(), "<task-notification>\n<task-id>worker-1</task-id>\n<agent>code</agent>\n<status>completed</status>\n<summary>done</summary>\n</task-notification>")
	if !messagesContain(l, "worker-1") {
		t.Fatal("a worker report waited for the background build")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for !messagesContain(l, "<background-command-notification>") {
		if time.Now().After(deadline) {
			t.Fatal("no completion notification")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	count := 0
	for _, m := range l.Messages {
		if strings.Contains(m.Content, "<background-command-notification>") {
			count++
			if !strings.Contains(m.Content, "<exit-code>0</exit-code>") || !strings.Contains(m.Content, "BUILD SUCCESSFUL") {
				t.Fatalf("notification = %q", m.Content)
			}
		}
	}
	if count != 1 {
		t.Fatalf("%d completion notifications, want 1", count)
	}
}

func messagesContain(l *Loop, text string) bool {
	l.backgroundMu.Lock()
	defer l.backgroundMu.Unlock()
	for _, m := range l.Messages {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}
