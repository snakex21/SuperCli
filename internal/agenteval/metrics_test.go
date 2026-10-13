package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBenchParsesUsageMetricsAndSummarizesPerModel(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "answer.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := `{"id":"s","name":"suite","tasks":[` +
		`{"id":"a","name":"a","prompt":"fix","workspaceFixture":"fixture","expectedChangedFiles":["answer.txt"],"requiredTraceEvents":["tool:write_file"],"verificationCommands":[{"id":"v","args":["go","version"]}]},` +
		`{"id":"b","name":"b","prompt":"fix","workspaceFixture":"fixture","expectedChangedFiles":["answer.txt"],"verificationCommands":[{"id":"v","args":["go","version"]}]}]}`
	suitePath := filepath.Join(root, "suite.json")
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Bench(context.Background(), BenchOptions{SuitePath: suitePath, WorkRoot: filepath.Join(root, "work"), Models: []string{"small", "big"}, Timeout: time.Minute, Runner: func(_ context.Context, input AgentInput) (AgentOutput, error) {
		if err := os.WriteFile(filepath.Join(input.Workspace, "answer.txt"), []byte(input.Model), 0o644); err != nil {
			return AgentOutput{}, err
		}
		if input.Model == "big" && input.TaskID == "b" {
			// No usage line: counts in runs/passed/duration only.
			return AgentOutput{Stdout: "done\n"}, nil
		}
		stdout := "Working on it {not json}\n" +
			`{"type":"tool","name":"write_file"}` + "\n" +
			// An earlier usage line must be superseded by the last one.
			`{"type":"usage","name":"run","input":1,"output":1}` + "\n" +
			"text with {\"type\":\"usage\"} inside\n" +
			`{"type":"usage","name":"run","model":"` + input.Model + `","input":1000,"output":200,"cached":600,"cacheWrite":100,"reasoning":5,"steps":3,"requests":4,"toolCalls":2,"ttftMs":120,"ttftAvgMs":150,"durationMs":900}` + "\n"
		return AgentOutput{Stdout: stdout}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 4 || report.Passed != 4 {
		t.Fatalf("report = %+v", report)
	}
	for _, run := range report.Runs {
		if run.Model == "big" && run.TaskID == "b" {
			if run.Metrics != nil {
				t.Fatalf("run without usage line got metrics %+v", run.Metrics)
			}
			continue
		}
		m := run.Metrics
		if m == nil || m.Input != 1000 || m.Cached != 600 || m.CacheWrite != 100 || m.Steps != 3 || m.Requests != 4 || m.ToolCalls != 2 || m.TTFTMs != 120 || m.Model != run.Model {
			t.Fatalf("%s/%s metrics = %+v", run.TaskID, run.Model, m)
		}
	}
	if len(report.Summary) != 2 || report.Summary[0].Model != "small" || report.Summary[1].Model != "big" {
		t.Fatalf("summary = %+v", report.Summary)
	}
	small, big := report.Summary[0], report.Summary[1]
	if small.Runs != 2 || small.Passed != 2 || small.MetricRuns != 2 || small.InputTotal != 2000 || small.CachedTotal != 1200 || small.CacheWriteTotal != 200 || small.InputMean != 1000 || small.OutputMean != 200 {
		t.Fatalf("small = %+v", small)
	}
	if small.CacheHitRatio != 0.6 || small.TTFTMeanMs != 120 || small.RequestsMean != 4 || small.ToolCallsMean != 2 || small.StepsMean != 3 {
		t.Fatalf("small means = %+v", small)
	}
	if big.Runs != 2 || big.Passed != 2 || big.MetricRuns != 1 || big.InputTotal != 1000 || big.InputMean != 1000 || big.StepsMean != 3 {
		t.Fatalf("big = %+v", big)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	values := []int64{50, 10, 40, 20, 30, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200}
	if got := percentile(values, 0.95); got != 190 {
		t.Fatalf("p95 = %d, want 190", got)
	}
	if got := percentile([]int64{7}, 0.95); got != 7 {
		t.Fatalf("p95 single = %d, want 7", got)
	}
	if got := percentile(nil, 0.95); got != 0 {
		t.Fatalf("p95 empty = %d, want 0", got)
	}
}
