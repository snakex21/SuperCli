package app

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func TestStatsCallSinkWorkerContext(t *testing.T) {
	rec := stats.NewMemory()
	sink := statsCallSink(rec)
	sink(llm.CallStat{Purpose: llm.PurposeMain, WorkerReportMessages: 2, WorkerReportBytes: 123})
	sink(llm.CallStat{Purpose: llm.PurposeMain})
	calls := rec.Calls()
	if len(calls) != 2 || calls[0].WorkerContext == nil || *calls[0].WorkerContext != (stats.WorkerContext{Requests: 1, MessageExposures: 2, ContextBytes: 123}) || calls[1].WorkerContext != nil {
		t.Fatalf("worker-context call mapping = %+v", calls)
	}
}
