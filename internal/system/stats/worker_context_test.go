package stats

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestWorkerContextMemoryDetachedAndJSONRoundtrip(t *testing.T) {
	m := NewMemory()
	m.RecordWorkerContext(WorkerContext{AddedMessages: 99}) // outside step ignored
	m.StartStep(1)
	m.RecordWorkerContext(WorkerContext{AddedMessages: 1, AddedBytes: 7})
	m.RecordWorkerContext(WorkerContext{AddedMessages: 2, AddedBytes: 13})
	exposure := WorkerContext{Requests: 2, MessageExposures: 6, ContextBytes: 40}
	m.RecordCall(Call{WorkerContext: &exposure})
	exposure.ContextBytes = 999
	m.EndStep()
	want := WorkerContext{AddedMessages: 3, AddedBytes: 20, Requests: 2, MessageExposures: 6, ContextBytes: 40}
	turns, calls := m.Snapshot(), m.Calls()
	if got := SumWorkerContext(turns, calls); got != want {
		t.Fatalf("sum = %+v", got)
	}
	turns[0].WorkerContext.AddedMessages = 999
	calls[0].WorkerContext.ContextBytes = 999
	if got := SumWorkerContext(m.Snapshot(), m.Calls()); got != want {
		t.Fatalf("aliased recorder: %+v", got)
	}
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := Save(path, m.Snapshot(), m.Calls()); err != nil {
		t.Fatal(err)
	}
	loadedTurns, loadedCalls, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := SumWorkerContext(loadedTurns, loadedCalls); got != want {
		t.Fatalf("roundtrip = %+v", got)
	}
	loadedTurns[0].WorkerContext.AddedBytes = 999
	loadedCalls[0].WorkerContext.Requests = 999
	if got := SumWorkerContext(m.Snapshot(), m.Calls()); got != want {
		t.Fatalf("aliased load: %+v", got)
	}
	var old struct {
		Turns []Turn `json:"turns"`
		Calls []Call `json:"calls"`
	}
	if err := json.Unmarshal([]byte(`{"turns":[{"step":1}],"calls":[{"purpose":"main"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if !SumWorkerContext(old.Turns, old.Calls).Empty() || old.Turns[0].WorkerContext != nil || old.Calls[0].WorkerContext != nil {
		t.Fatal("legacy stats changed")
	}
}
