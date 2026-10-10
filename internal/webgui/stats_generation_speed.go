package webgui

import "supercli/internal/storage/session"

// The session average is a ratio of matching measured samples, not an average
// of per-call rates. It includes all real model calls in the conversation,
// regardless of model switches or purpose, without counting reasoning twice.
type statsGenerationSpeedView struct {
	Scope           string   `json:"scope"`
	TokensPerSecond *float64 `json:"tokens_per_second"`
	OutputTokens    int64    `json:"output_tokens"`
	StreamMS        int64    `json:"stream_ms"`
	Samples         int      `json:"samples"`
	Calls           int      `json:"calls"`
}

// HasTiming excludes failed/canceled calls at capture. A positive TTFT is
// required to distinguish an observed stream start from older unknown clocks.
// Provider output already includes reasoning; cache is an input subset.
func measuredUsageStream(u session.UsageRecord) (output, milliseconds int64, known bool) {
	if u.Source == "legacy" || !u.HasTiming || u.DurationMS <= 0 ||
		u.TTFTMS <= 0 || u.TTFTMS >= u.DurationMS || u.Output <= 0 {
		return 0, 0, false
	}
	return u.Output, u.DurationMS - u.TTFTMS, true
}

func (s *statsGenerationSpeedView) add(u session.UsageRecord) {
	if u.Source == "legacy" {
		return
	}
	s.Calls++
	if output, milliseconds, known := measuredUsageStream(u); known {
		s.OutputTokens += output
		s.StreamMS += milliseconds
		s.Samples++
	}
}

func (s *statsGenerationSpeedView) finish() {
	s.Scope = "session"
	if s.Samples > 0 && s.StreamMS > 0 {
		rate := float64(s.OutputTokens) * 1000 / float64(s.StreamMS)
		s.TokensPerSecond = &rate
	}
}
