package agenteval

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// RunMetrics mirrors the final `{"type":"usage","name":"run"}` line
// that `supercli --batch --trace-jsonl` writes to stdout.
type RunMetrics struct {
	Model      string  `json:"model,omitempty"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Cached     int     `json:"cached"`
	CacheWrite int     `json:"cacheWrite"`
	Reasoning  int     `json:"reasoning"`
	Steps      int     `json:"steps"`
	Requests   int     `json:"requests"`
	ToolCalls  int     `json:"toolCalls"`
	TTFTMs     float64 `json:"ttftMs"`
	TTFTAvgMs  float64 `json:"ttftAvgMs"`
	DurationMs int64   `json:"durationMs"`
	Error      bool    `json:"error,omitempty"`
}

// ModelSummary aggregates every run of one model. Runs, Passed and
// the duration figures (harness wall time) cover all runs; token,
// latency and step figures cover only MetricRuns — runs whose agent
// reported a usage line.
type ModelSummary struct {
	Model           string  `json:"model"`
	Runs            int     `json:"runs"`
	Passed          int     `json:"passed"`
	MetricRuns      int     `json:"metricRuns"`
	InputTotal      int     `json:"inputTotal"`
	OutputTotal     int     `json:"outputTotal"`
	CachedTotal     int     `json:"cachedTotal"`
	CacheWriteTotal int     `json:"cacheWriteTotal"`
	InputMean       float64 `json:"inputMean"`
	OutputMean      float64 `json:"outputMean"`
	CachedMean      float64 `json:"cachedMean"`
	CacheWriteMean  float64 `json:"cacheWriteMean"`
	CacheHitRatio   float64 `json:"cacheHitRatio"`
	DurationMeanMs  float64 `json:"durationMeanMs"`
	DurationP95Ms   int64   `json:"durationP95Ms"`
	TTFTMeanMs      float64 `json:"ttftMeanMs"`
	RequestsMean    float64 `json:"requestsMean"`
	ToolCallsMean   float64 `json:"toolCallsMean"`
	StepsMean       float64 `json:"stepsMean"`
}

// parseUsage returns the LAST usage line in stdout (a retried or
// nested run may print several; the final one is the run total).
// Lines that are not JSON objects — assistant text — are ignored.
func parseUsage(stdout string) *RunMetrics {
	var last *RunMetrics
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var head struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(line), &head) != nil || head.Type != "usage" || head.Name != "run" {
			continue
		}
		var m RunMetrics
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		last = &m
	}
	return last
}

// summarize groups runs per model in order of first appearance.
func summarize(runs []RunReport) []ModelSummary {
	type acc struct {
		sum       ModelSummary
		durations []int64
		ttftSum   float64
		ttftCount int
		requests  int
		toolCalls int
		steps     int
	}
	byModel := map[string]*acc{}
	var order []string
	for _, run := range runs {
		a, ok := byModel[run.Model]
		if !ok {
			a = &acc{sum: ModelSummary{Model: run.Model}}
			byModel[run.Model] = a
			order = append(order, run.Model)
		}
		a.sum.Runs++
		if run.Passed {
			a.sum.Passed++
		}
		a.durations = append(a.durations, run.DurationMs)
		m := run.Metrics
		if m == nil {
			continue
		}
		a.sum.MetricRuns++
		a.sum.InputTotal += m.Input
		a.sum.OutputTotal += m.Output
		a.sum.CachedTotal += m.Cached
		a.sum.CacheWriteTotal += m.CacheWrite
		a.requests += m.Requests
		a.toolCalls += m.ToolCalls
		a.steps += m.Steps
		if m.TTFTMs > 0 {
			a.ttftSum += m.TTFTMs
			a.ttftCount++
		}
	}
	out := make([]ModelSummary, 0, len(order))
	for _, model := range order {
		a := byModel[model]
		s := a.sum
		var durSum int64
		for _, d := range a.durations {
			durSum += d
		}
		s.DurationMeanMs = float64(durSum) / float64(len(a.durations))
		s.DurationP95Ms = percentile(a.durations, 0.95)
		if n := float64(s.MetricRuns); n > 0 {
			s.InputMean = float64(s.InputTotal) / n
			s.OutputMean = float64(s.OutputTotal) / n
			s.CachedMean = float64(s.CachedTotal) / n
			s.CacheWriteMean = float64(s.CacheWriteTotal) / n
			s.RequestsMean = float64(a.requests) / n
			s.ToolCallsMean = float64(a.toolCalls) / n
			s.StepsMean = float64(a.steps) / n
		}
		if s.InputTotal > 0 {
			s.CacheHitRatio = float64(s.CachedTotal) / float64(s.InputTotal)
		}
		if a.ttftCount > 0 {
			s.TTFTMeanMs = a.ttftSum / float64(a.ttftCount)
		}
		out = append(out, s)
	}
	return out
}

// percentile is the nearest-rank percentile (p in (0,1]).
func percentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
