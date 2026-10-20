package stats

import (
	"encoding/json"
	"sync"
	"testing"

	"supercli/internal/llm"
)

func TestContextUsageCountersDetachedConcurrent(t *testing.T) {
	m := NewMemory()
	m.StartStep(1)
	message := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: "private ą🙂"}})
	added := llm.ContextAddition(message)
	measured := llm.MeasureContext([]llm.Message{message}, nil)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { m.RecordContextUsage(added); m.RecordCall(Call{Context: &measured}) })
	}
	wg.Wait()
	m.EndStep()
	turns, calls := m.Snapshot(), m.Calls()
	u := SumContextUsage(turns, calls)
	if u.MeasuredCalls != 12 || u.Sources[llm.SourceHuman].AddedItems != 12 || u.Sources[llm.SourceHuman].Exposures != 12 {
		t.Fatalf("sum=%+v", u)
	}
	u.Sources[llm.SourceHuman] = llm.ContextMeasure{}
	turns[0].Context.Sources[llm.SourceHuman] = llm.ContextMeasure{}
	calls[0].Context.Sources[llm.SourceHuman] = llm.ContextMeasure{}
	if SumContextUsage(m.Snapshot(), m.Calls()).Sources[llm.SourceHuman].AddedItems != 12 {
		t.Fatal("snapshots alias recorder maps")
	}
	raw, err := json.Marshal(struct {
		Turns []Turn
		Calls []Call
	}{m.Snapshot(), m.Calls()})
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip struct {
		Turns []Turn
		Calls []Call
	}
	if err = json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if SumContextUsage(roundtrip.Turns, roundtrip.Calls).MeasuredCalls != 12 {
		t.Fatal("JSON lost counters")
	}
	if SumContextUsage([]Turn{{}}, []Call{{}}) != nil {
		t.Fatal("legacy fabricated telemetry")
	}
}
