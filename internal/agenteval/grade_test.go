package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "pkg/main.go", false},
		{"pkg/**/*_test.go", "pkg/a/b/x_test.go", true},
		{"pkg/**/*_test.go", "pkg/x_test.go", true},
		{"pkg/**/*_test.go", "pkg/a/x.go", false},
		{"modules/**", "modules/a/b.go", true},
		{"**/__pycache__/**", "tests/__pycache__/x.pyc", true},
		{"data files/**", "data files/input one.txt", true},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v", c.pattern, c.name, got)
		}
	}
}

func TestChangedLineCount(t *testing.T) {
	if n := ChangedLineCount("a\nb\nc\n", "a\nB\nc\n"); n != 2 {
		t.Fatalf("one replaced line = %d, want 2", n)
	}
	if n := ChangedLineCount("a\r\nb\r\n", "a\nb\n"); n != 0 {
		t.Fatalf("line endings only = %d, want 0", n)
	}
	if n := ChangedLineCount("a\nb\n", "x\na\nb\ny\n"); n != 2 {
		t.Fatalf("two added = %d, want 2", n)
	}
}

func TestExtractAnswerDropsThinkingAndTrace(t *testing.T) {
	out := "<thinking>We</thinking>\n<thinking> need\nmore</thinking>\n{\"type\":\"tool\",\"name\":\"read\"}\nThe answer is 42.\n{\"type\":\"usage\",\"name\":\"run\"}\n"
	if got := ExtractAnswer(out); got != "The answer is 42." {
		t.Fatalf("answer = %q", got)
	}
}

func TestGradeAnswerPatterns(t *testing.T) {
	answer := `Fixed in internal\limits\bucket.go: (*Buckets).Allow`
	if r := gradeAnswer(answer, AnswerCheck{ID: "f", AnyOf: []string{"internal/limits/bucket.go"}}); !r.Passed {
		t.Fatalf("backslash path not normalized: %+v", r)
	}
	if r := gradeAnswer(answer, AnswerCheck{ID: "m", AllOf: []string{`\bAllow\b`, "missing"}, Regex: true}); r.Passed {
		t.Fatal("allOf should fail")
	}
	if r := gradeAnswer(answer, AnswerCheck{ID: "n", NoneOf: []string{"legacy"}}); !r.Passed {
		t.Fatal("noneOf should pass")
	}
}

func TestDecodeTextRejectsUTF16(t *testing.T) {
	if _, problem := decodeText([]byte{0xFF, 0xFE, 'a', 0}); problem == "" {
		t.Fatal("UTF-16 must be reported")
	}
	if text, problem := decodeText([]byte{0xEF, 0xBB, 0xBF, 'o', 'k'}); problem != "" || text != "ok" {
		t.Fatalf("BOM UTF-8: %q %q", text, problem)
	}
}

// TestBenchNestedLayoutGradersAndRepeats covers the agent-suite
// features: hidden grader overlay, strict diff, outside-write detection,
// file/answer checks, partial status and repeats.
func TestBenchNestedLayoutGradersAndRepeats(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fixture/a.txt", "one\ntwo\n")
	write("fixture/keep.txt", "keep\n")
	write("overlay/hidden.txt", "hidden\n")
	write("suite.json", `{"id":"s","name":"n","ignoreChangedFiles":["**/*.tmp"],"tasks":[
	 {"id":"ok","name":"ok","prompt":"p","category":"c","workspaceFixture":"fixture","graderOverlay":"overlay","strictDiff":true,"allowedChangedFiles":["a.txt"],
	  "fileChecks":[{"id":"edit","path":"a.txt","contains":["TWO"],"maxChangedLines":2,"critical":true},{"id":"overlay","path":"hidden.txt"}],
	  "answerChecks":[{"id":"fact1","anyOf":["alpha"]},{"id":"fact2","anyOf":["beta"]},{"id":"fact3","anyOf":["gamma"]}]},
	 {"id":"escape","name":"escape","prompt":"p","workspaceFixture":"fixture","strictDiff":true,"allowedChangedFiles":["a.txt"],
	  "fileChecks":[{"id":"edit","path":"a.txt","contains":["TWO"]}]}]}`)
	report, err := Bench(context.Background(), BenchOptions{SuitePath: filepath.Join(root, "suite.json"), WorkRoot: filepath.Join(root, "work"), Timeout: time.Minute,
		NestWorkspace: true, Repeats: 2, Runner: func(_ context.Context, in AgentInput) (AgentOutput, error) {
			if in.DataDir == "" || filepath.Dir(in.Workspace) != in.RunDir {
				t.Errorf("nested layout not passed: %+v", in)
			}
			if _, err := os.Stat(filepath.Join(in.Workspace, "hidden.txt")); err == nil {
				t.Error("grader overlay visible to the agent")
			}
			_ = os.WriteFile(filepath.Join(in.Workspace, "a.txt"), []byte("one\nTWO\n"), 0o644)
			_ = os.WriteFile(filepath.Join(in.Workspace, "scratch.tmp"), []byte("x"), 0o644)
			_ = os.WriteFile(filepath.Join(in.DataDir, "session.db"), []byte("x"), 0o644)
			if in.TaskID == "escape" {
				_ = os.MkdirAll(filepath.Join(in.RunDir, "reports"), 0o755)
				_ = os.WriteFile(filepath.Join(in.RunDir, "reports", "out.csv"), []byte("x"), 0o644)
			}
			return AgentOutput{Answer: "alpha and beta"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 4 || report.Runs[0].Repeat != 1 || report.Runs[2].Repeat != 2 {
		t.Fatalf("repeat order: %+v", report.Runs)
	}
	ok := report.Runs[0]
	if ok.Status != StatusPartial || len(ok.ChangedFiles) != 1 || ok.FailureCategory != FailAnswer {
		t.Fatalf("ok run = %+v", ok)
	}
	escape := report.Runs[1]
	if escape.Status != StatusFail || escape.FailureCategory != FailOutsideWrite || len(escape.OutsideWrites) != 1 || escape.OutsideWrites[0] != "reports/out.csv" {
		t.Fatalf("escape run = %+v", escape)
	}
}
