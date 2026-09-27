package ctxexec

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFailureSummaryCompleteCaptureIgnoresPreviewLimit(t *testing.T) {
	for _, capKB := range []int{1, 4, 16, 64} {
		result, err := New(t.TempDir()).Run(context.Background(), &Request{
			Command:     []string{os.Args[0], "-test.run=^TestCaptureHelper$"},
			EnvExtra:    []string{"SUPERCLI_CAPTURE_HELPER=medium_failure"},
			MaxStdoutKB: capKB, MaxStderrKB: capKB, TimeoutMS: 5000,
		})
		if err != nil || result.ExitCode != 7 {
			t.Fatalf("run cap=%d: %+v %v", capKB, result, err)
		}
		if (result.retained == nil) != (capKB >= 16) {
			t.Fatalf("fixture retention incorrect for cap=%d", capKB)
		}
		before, _ := json.Marshal(result)
		got := result.FailureSummary()
		if strings.Count(got, "EARLY_EVIDENCE") != 2 || strings.Count(got, "FINAL_EVIDENCE") != 2 {
			t.Errorf("cap=%d lost stream endpoints: %s", capKB, got)
		}
		if len(got) > 2*FailTailBytes+200 || !utf8.ValidString(got) {
			t.Errorf("cap=%d invalid/bloated summary: %d bytes", capKB, len(got))
		}
		after, _ := json.Marshal(result)
		if string(before) != string(after) {
			t.Fatal("summary modified raw evidence")
		}
	}
}

func TestFailureSummarySavedCompleteCaptures(t *testing.T) {
	path := os.Getenv("SUPERCLI_FAILURE_CAPTURE_REPLAY")
	if path == "" {
		t.Skip("optional fixed saved outputs")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct{ Handle, Content string }
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("saved fixtures=%d want2", len(entries))
	}
	for _, entry := range entries {
		var result Result
		if err := json.Unmarshal([]byte(entry.Content), &result); err != nil {
			t.Fatal(err)
		}
		if result.ExitCode == 0 || result.TruncatedStdout || len(result.Stdout) <= FailTailBytes {
			t.Fatal("saved result does not expose complete-capture gap")
		}
		first := strings.SplitN(result.Stdout, "\n", 2)[0]
		got := result.FailureSummary()
		if !strings.Contains(got, first) {
			t.Errorf("%s lost first diagnostic/header %q", entry.Handle, first)
		}
		if len(got) > 2*FailTailBytes+200 || !utf8.ValidString(got) {
			t.Error("invalid summary")
		}
		t.Logf("handle=%s full_stdout=%d summary=%d first=%q", entry.Handle, len(result.Stdout), len(got), first)
	}
}
