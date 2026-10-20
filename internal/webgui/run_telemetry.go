package webgui

import (
	"supercli/internal/llm"
	systats "supercli/internal/system/stats"
)

// telemetryCallSink copies counters and timings that llm.Metered already
// computed. It never reads prompt text and performs no estimation or I/O.
func telemetryCallSink(rec systats.Recorder) llm.CallSink {
	if rec == nil {
		return nil
	}
	return func(s llm.CallStat) {
		var workerContext *systats.WorkerContext
		if s.WorkerReportMessages > 0 {
			workerContext = &systats.WorkerContext{Requests: 1, MessageExposures: s.WorkerReportMessages, ContextBytes: s.WorkerReportBytes}
		}
		rec.RecordCall(systats.Call{
			Context:       llm.CloneContextUsage(&s.Context),
			WorkerContext: workerContext,
			Purpose:       s.Purpose, Model: s.Model, Provider: s.Provider,
			Background: s.Background, Canceled: s.Canceled, Failed: s.Failed,
			TTFTUs: s.TTFT.Microseconds(), DurationUs: s.Duration.Microseconds(),
			TokensIn: s.TokensIn, TokensOut: s.TokensOut, TokensCached: s.TokensCached, TokensReasoning: s.TokensReasoning, StartedAt: s.StartedAt,
		})
	}
}

func summarizeTelemetryCalls(calls []systats.Call) (failed, canceled, background, helper int) {
	for _, call := range calls {
		if call.Failed {
			failed++
		}
		if call.Canceled {
			canceled++
		}
		if call.Background {
			background++
		}
		if call.Purpose != "" && call.Purpose != llm.PurposeMain {
			helper++
		}
	}
	return
}
