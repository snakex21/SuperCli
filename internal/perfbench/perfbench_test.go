package perfbench

import "testing"

func TestSummarizeNearestRankP95(t *testing.T) {
	got := Summarize([]float64{5, 1, 3, 2, 4})
	if got.Min != 1 || got.Median != 3 || got.Average != 3 || got.P95 != 5 || got.Max != 5 {
		t.Fatalf("%+v", got)
	}
}

func TestWarningsUseConfiguredThresholds(t *testing.T) {
	m := Metrics{ColdStartMs: Stats{P95: 20}, FirstOutputMs: Stats{P95: 10}, PeakRSSMB: Stats{Max: 50}}
	if got := warnings(m, Thresholds{ColdStartP95Ms: 19, FirstOutputP95Ms: 20, PeakRSSMaxMB: 60}); len(got) != 1 || got[0].Metric != "coldStartMs" {
		t.Fatalf("%+v", got)
	}
}
