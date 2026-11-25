package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// The reported brief (anonymized): shared project preamble, then the task.
const reportedBrief = "Projekt: port Minecraft Java 1.12 do Zig. Przeczytaj AGENTS.md.\nCEL: rozdziel view distance na loader chunków i renderer\nPliki: src/world/view_distance.zig"

// requestHasBrief: the worker's request carries the pinned brief.
func requestHasBrief(msgs []llm.Message) bool {
	for _, m := range msgs {
		if m.Role == llm.RoleSystem && strings.Contains(m.Content, workerPinnedBriefPrefix) && strings.Contains(m.Content, "rozdziel view distance") {
			return true
		}
	}
	return false
}

func requestHas(msgs []llm.Message, text string) bool {
	for _, m := range msgs {
		if strings.Contains(m.TextOnly().Content, text) {
			return true
		}
	}
	return false
}

// A worker whose FIRST request failed (model not found), resumed after the
// error, after a worker-model change and after its history was compacted:
// every request carries the original brief, and later ones its history.
func TestResumedWorkerAlwaysSeesItsBrief(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"agent":"code","prompt":"`+strings.ReplaceAll(reportedBrief, "\n", `\n`)+`"}`)), say("Delegated."),
			callStep(tcall("s1", "send_message", `{"to":"worker-1","message":"Limit API minął, kontynuuj."}`)), say("Resumed."),
			callStep(tcall("s2", "send_message", `{"to":"worker-1","message":"Kontynuuj na nowym modelu."}`)), say("Resumed."),
			callStep(tcall("s3", "send_message", `{"to":"worker-1","message":"Kontynuuj po kompakcji."}`)), say("Resumed."),
		},
		[][]llm.Delta{
			{{Err: errors.New(`http 404: {"type":"not_found_error","message":"model: ja/google/gemini-4-neon"}`)}},
			say("WORKED_ON_SPLIT: first part done"),
		})
	runTurnText(t, f.parent, "Rozdziel view distance.")
	w := f.task.Workers.List()[0]
	if s := w.Snapshot(); s.Status != "failed" {
		t.Fatalf("setup: first run status %s", s.Status)
	}

	// 1. Resume after the failed first request.
	before := len(f.child.reqs)
	runTurnText(t, f.parent, "wznów worker-1")
	if len(f.child.reqs) == before || !requestHasBrief(f.child.reqs[len(f.child.reqs)-1]) {
		t.Fatal("resume after a failed first request: the worker did not see its brief")
	}

	// 2. Resume on another worker model.
	b := &stubProvider{name: "model-b", scripts: [][]llm.Delta{say("WORKED_ON_SPLIT: second part on B")}}
	f.task.WorkerProvider = b
	w.setState(func(w *Worker) { w.Status, w.LastError = "failed", usageLimitError })
	runTurnText(t, f.parent, "wznów worker-1")
	if len(b.reqs) != 1 || !requestHasBrief(b.reqs[0]) || !requestHas(b.reqs[0], "WORKED_ON_SPLIT: first part done") {
		t.Fatal("resume on a new model lost the brief or the prior history")
	}

	// 3. Resume after the worker's own history was compacted away.
	lead := leadingSystemCount(w.Loop.Messages)
	w.Loop.Messages = append(w.Loop.Messages[:lead:lead], llm.Message{Role: llm.RoleUser, Content: WrapCompactSummary("Goal: continue. Done: two parts.")})
	w.Loop.hidden = nil
	w.Loop.invalidateVisibleEstimate()
	b.scripts = append(b.scripts, say("WORKED_ON_SPLIT: third part"))
	w.setState(func(w *Worker) { w.Status, w.LastError = WorkerStatusInterrupted, "runner ended" })
	runTurnText(t, f.parent, "wznów worker-1")
	if len(b.reqs) != 2 || !requestHasBrief(b.reqs[1]) {
		t.Fatal("resume after compaction lost the brief")
	}
}

// After an app restart the worker is rebuilt from its saved record; its
// brief is pinned again from the record's description.
func TestRestoredWorkerSeesItsBrief(t *testing.T) {
	p := &stubProvider{name: "restored", scripts: [][]llm.Delta{say("continued")}}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), MaxSteps: 3,
		InitialMessages: []llm.Message{{Role: llm.RoleUser, Content: "Limit API minął, wznów pracę od miejsca przerwania."}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewWorkerRegistry()
	w, err := reg.addRestored(WorkerRecord{ID: "worker-374", Agent: "code", Description: reportedBrief, Status: WorkerStatusInterrupted}, loop)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runWorkerInvocation(context.Background(), w, "Kontynuuj.", reg); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 1 || !requestHasBrief(p.reqs[0]) || !requestHas(p.reqs[0], "Limit API minął") {
		t.Fatal("a restored worker lost its brief or its saved history")
	}
}

// Reports, failures and status listings name each worker's task, so the
// coordinator never maps ids to tasks by call order.
func TestCoordinatorSeesWorkerTaskTitles(t *testing.T) {
	if got := workerTaskTitle(reportedBrief); got != "rozdziel view distance na loader chunków i renderer" {
		t.Fatalf("title = %q", got)
	}
	if got := workerTaskTitle("Napraw łuk gracza w PlayerBow.zig"); got != "Napraw łuk gracza w PlayerBow.zig" {
		t.Fatalf("title without a goal line = %q", got)
	}
	s := Snapshot{ID: "worker-377", Agent: "code", Status: "failed", Description: "Projekt: port.\nGOAL: łuk gracza (napinanie i strzał)"}
	if sum := workerSummaryFromSnapshot(s); !strings.Contains(sum, "task: łuk gracza (napinanie i strzał)") {
		t.Fatalf("report summary without the task: %q", sum)
	}
	if err := workerFailureHandoffCaptured(s, true, "", errors.New("http 429"), ""); !strings.Contains(err.Error(), "Task: łuk gracza") {
		t.Fatalf("failure handoff without the task: %q", err)
	}
	l := &Loop{}
	l.workerStatusSource = func() []Snapshot {
		return []Snapshot{s, {ID: "worker-374", Agent: "code", Status: "interrupted", Description: reportedBrief}}
	}
	listing := l.statusTurnContext()
	if !strings.Contains(listing, "łuk gracza") || !strings.Contains(listing, "rozdziel view distance") {
		t.Fatalf("status listing without titles:\n%s", listing)
	}
}
