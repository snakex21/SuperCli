package agent

import "testing"

func TestPreparedWorkerNotificationCarriesCapturedInvocation(t *testing.T) {
	events := make(chan Event, 1)
	parent := &Loop{}
	parent.SetExternalSink(events)
	// The handoff is already immutable; never consult the mutable worker when
	// delivering it, even if another invocation now owns that worker.
	h := workerHandoff{snapshot: Snapshot{ID: "worker-1", Agent: "code", Runs: 1, Status: "done"}, summary: "historical summary", notification: "historical report"}
	(&AgentTool{ParentLoop: parent}).emitPreparedWorkerNotification(h)
	got := (<-events).(WorkerNotificationEvent)
	if got.Run != 1 || got.Status != "done" || got.Text != h.notification || got.Summary != h.summary {
		t.Fatalf("captured notification changed: %+v", got)
	}
}
