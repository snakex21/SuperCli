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
		rec.RecordCall(stats.Call{
			Purpose:                s.Purpose,
			Model:                  s.Model,
			Provider:               s.Provider,
			Background:             s.Background,
			Canceled:               s.Canceled,
			Failed:                 s.Failed,
			TTFTUs:                 s.TTFT.Microseconds(),
			DurationUs:             s.Duration.Microseconds(),
			TokensIn:               s.TokensIn,
			TokensOut:              s.TokensOut,
			TokensCached:           s.TokensCached,
			PrefillEvaluated:       s.PrefillEvaluated,
			PrefillTokensPerSecond: s.PrefillTokensPerSecond,
			PrefillBudget:          s.PrefillBudget,
			PrefillBudgetSource:    s.PrefillBudgetSource,
			StartedAt:              s.StartedAt,
		})
	}
}
