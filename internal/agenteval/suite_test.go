package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBenchScoresFilesChecksAndModelMatrix(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "answer.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := `{"id":"s","name":"suite","tasks":[{"id":"edit","name":"edit","prompt":"fix","workspaceFixture":"fixture","expectedChangedFiles":["answer.txt"],"forbiddenChangedFiles":["keep.txt"],"requiredTraceEvents":["tool:write_file"],"verificationCommands":[{"id":"exists","args":["go","version"]}]}]}`
	suitePath := filepath.Join(root, "suite.json")
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Bench(context.Background(), BenchOptions{SuitePath: suitePath, WorkRoot: filepath.Join(root, "work"), Models: []string{"small", "big"}, Timeout: time.Minute, Runner: func(_ context.Context, input AgentInput) (AgentOutput, error) {
		if err := os.WriteFile(filepath.Join(input.Workspace, "answer.txt"), []byte(input.Model), 0o644); err != nil {
			return AgentOutput{}, err
		}
		return AgentOutput{Stdout: `{"type":"tool","name":"write_file"}` + "\n"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed != 2 || report.Failed != 0 || len(report.Runs) != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestLoadSuiteRejectsUnknownAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "suite.json")
	bad := `{"id":"s","name":"n","extra":true,"tasks":[{"id":"x","name":"x","prompt":"p","workspaceFixture":"../outside","verificationCommands":[{"id":"v","args":["go","version"]}]}]}`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSuite(path); err == nil {
		t.Fatal("expected strict decode error")
	}
}
