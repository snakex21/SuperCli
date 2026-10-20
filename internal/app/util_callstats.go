package app

import (
	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// statsCallSink adapts llm.Metered call records into the session's
// stats recorder, so /cost and the JSON snapshot show every model
// call with its purpose label. Nil recorder returns a nil sink,
// which disables the llm.Metered decorator entirely.
func statsCallSink(rec stats.Recorder) llm.CallSink {
	if rec == nil {
		return nil
	}
	return func(s llm.CallStat) {
		var workerContext *stats.WorkerContext
		if s.WorkerReportMessages > 0 {
			workerContext = &stats.WorkerContext{Requests: 1, MessageExposures: s.WorkerReportMessages, ContextBytes: s.WorkerReportBytes}
		}
		rec.RecordCall(stats.Call{
			Context:                llm.CloneContextUsage(&s.Context),
			WorkerContext:          workerContext,
			Purpose:                s.Purpose,
			Model:                  s.Model,
			Provider:               s.Provider,
			ProviderType:           s.ProviderType,
			EndpointHost:           s.EndpointHost,
			ConnectionKey:          s.ConnectionKey,
			Background:             s.Background,
			Canceled:               s.Canceled,
			Failed:                 s.Failed,
			TTFTUs:                 s.TTFT.Microseconds(),
			DurationUs:             s.Duration.Microseconds(),
			TokensIn:               s.TokensIn,
			TokensOut:              s.TokensOut,
			TokensCached:           s.TokensCached,
			TokensReasoning:        s.TokensReasoning,
			PrefillEvaluated:       s.PrefillEvaluated,
			PrefillTokensPerSecond: s.PrefillTokensPerSecond,
			PrefillBudget:          s.PrefillBudget,
			PrefillBudgetSource:    s.PrefillBudgetSource,
			StartedAt:              s.StartedAt,
		})
	}
}
