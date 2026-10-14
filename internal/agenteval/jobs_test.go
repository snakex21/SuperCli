package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBenchJobsRunSideBySideAndKeepOrder(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "answer.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := `"verificationCommands":[{"id":"v","args":["go","version"]}]`
	suite := `{"id":"s","name":"suite","tasks":[{"id":"a","name":"a","prompt":"p","workspaceFixture":"fixture","expectedChangedFiles":["answer.txt"],` + check + `},{"id":"b","name":"b","prompt":"p","workspaceFixture":"fixture","expectedChangedFiles":["answer.txt"],` + check + `}]}`
	suitePath := filepath.Join(root, "suite.json")
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	var running, peak int32
	report, err := Bench(context.Background(), BenchOptions{SuitePath: suitePath, WorkRoot: filepath.Join(root, "work"), Models: []string{"m1", "m2"}, Timeout: time.Minute, Jobs: 4, Runner: func(_ context.Context, input AgentInput) (AgentOutput, error) {
		n := atomic.AddInt32(&running, 1)
		defer atomic.AddInt32(&running, -1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		return AgentOutput{}, os.WriteFile(filepath.Join(input.Workspace, "answer.txt"), []byte(input.TaskID+input.Model), 0o644)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 4 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if peak < 2 {
		t.Fatalf("runs did not overlap: peak=%d", peak)
	}
	want := []string{"a/m1", "a/m2", "b/m1", "b/m2"}
	for i, run := range report.Runs {
		if got := run.TaskID + "/" + run.Model; got != want[i] {
			t.Fatalf("run %d = %s, want %s", i, got, want[i])
		}
	}
}
