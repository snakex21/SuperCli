package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Review 3: "tests pass" relayed from workers was flagged as unverified when
// the worker's command was beyond the first 32 tools, after a resume (the
// tool list is cleared) or after a restart (saved workers have no tool list).
// The lifetime command/check counters are saved and never cleared.
func TestWorkerCommandCountersSurviveToolCapResumeAndRestart(t *testing.T) {
	var calls []llm.Delta
	for i := 0; i < 33; i++ {
		calls = append(calls, tcall(fmt.Sprintf("e%d", i), "echo", `{}`))
	}
	calls = append(calls, tcall("cmd", "ctx_execute", `{"command":["go","test","./..."]}`))
	f, _ := contractFixture(t, true,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"fix the parser"}`)), say("Delegated.")},
		[][]llm.Delta{callStep(calls...), say("Fixed; tests pass."), say("Checked again.")})
	for _, name := range []string{"echo", "ctx_execute"} {
		f.parent.registry.MustRegister(tools.Tool{Name: name, Description: name, Schema: `{"type":"object"}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	}
	f.parent.writer = &sessionWriterWithID{id: "sess-cmds"}
	store := briefStore{newMemWorkerStore()}
	f.task.Workers.SetStore(store, nil)

	runTurnWithin(t, f.parent, "Napraw parser.", 3*time.Second)
	w := f.task.Workers.List()[0]
	s := waitWorker(t, w, "first run", func(s Snapshot) bool { return s.Status == "done" })
	if len(s.ToolNames) != 32 || s.CommandsRun != 1 || s.ChecksRun != 1 {
		t.Fatalf("after 33+1 tools: tools=%d commands=%d checks=%d", len(s.ToolNames), s.CommandsRun, s.ChecksRun)
	}
	if !f.parent.workersRanCommands() {
		t.Fatal("a command beyond the first 32 tools was not seen")
	}

	// Resume: the new run's tool list starts empty, the counters do not.
	// The finished run releases the worker just after its status turns done.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		_, err := runWorkerInvocation(context.Background(), w, "check again", f.task.Workers)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	s = waitWorker(t, w, "resumed run", func(s Snapshot) bool { return s.Status == "done" && s.Runs == 2 })
	if len(s.ToolNames) != 0 || s.CommandsRun != 1 {
		t.Fatalf("after resume: tools=%v commands=%d", s.ToolNames, s.CommandsRun)
	}
	if !f.parent.workersRanCommands() {
		t.Fatal("a resume forgot the worker's earlier command")
	}

	// Restart: a fresh process sees the saved worker only through the store.
	g, _ := contractFixture(t, true, nil, nil)
	g.parent.writer = &sessionWriterWithID{id: "sess-cmds"}
	g.task.Workers.SetStore(store, []string{w.ID})
	if len(g.task.Workers.List()) != 0 {
		t.Fatal("fixture: the new registry should start empty")
	}
	if !g.parent.workersRanCommands() {
		t.Fatal("after a restart the saved worker's commands were forgotten")
	}
	rec, ok, err := store.LoadWorker(context.Background(), "sess-cmds", w.ID)
	if err != nil || !ok || rec.CommandsRun != 1 || rec.ChecksRun != 1 {
		t.Fatalf("saved record = %+v ok=%v err=%v", rec, ok, err)
	}
}
