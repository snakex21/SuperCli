package ctxexec

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPromptLine(t *testing.T) {
	prompts := []string{
		"Overwrite existing file? [y/N] ",
		"Continue (y/n)",
		"? Select a model to load (Use arrow keys)\n❯ qwen3.8-27b\n  other",
		"\x1b[32m?\x1b[0m Choose a model:",
		"Enter passphrase for key 'id_ed25519': ",
		"progress 10%\rprogress 99%\rPassword: ",
		"Press any key to continue . . .",
		"Do you want to proceed?",
	}
	for _, p := range prompts {
		if promptLine(p) == "" {
			t.Errorf("not detected as prompt: %q", p)
		}
	}
	quiet := []string{
		"",
		"building packages\n",
		"ok  \tsupercli/internal/app\t12.3s\n",
		"?   \tsupercli/experiments/sciter\t[no test files]\n",
		"Downloading 45%",
		"=== RUN   TestFoo\n",
		"Compiling crate:\n",
		"Results:",
	}
	for _, q := range quiet {
		if got := promptLine(q); got != "" {
			t.Errorf("false prompt %q from %q", got, q)
		}
	}
}

func TestRunnerStopsCommandWaitingForInput(t *testing.T) {
	r := New(t.TempDir())
	r.PromptIdle = 300 * time.Millisecond
	res, err := r.Run(context.Background(), &Request{
		Command:  []string{os.Args[0], "-test.run=^TestCaptureHelper$"},
		EnvExtra: []string{"SUPERCLI_CAPTURE_HELPER=prompt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitTimeout || !strings.HasPrefix(res.Error, PromptStallPrefix) {
		t.Fatalf("exit=%d error=%q", res.ExitCode, res.Error)
	}
	if res.DurationMS > 2500 {
		t.Fatalf("watchdog too slow: %d ms", res.DurationMS)
	}
	if s := res.FailureSummary(); !strings.Contains(s, "--yes") || !strings.Contains(s, "[y/N]") {
		t.Fatalf("summary lacks hint: %s", s)
	}
}

func TestRunnerKeepsSilentNonPromptCommand(t *testing.T) {
	r := New(t.TempDir())
	r.PromptIdle = 300 * time.Millisecond
	res, err := r.Run(context.Background(), &Request{
		Command:  []string{os.Args[0], "-test.run=^TestCaptureHelper$"},
		EnvExtra: []string{"SUPERCLI_CAPTURE_HELPER=quiet"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Error != "" {
		t.Fatalf("silent build was stopped: exit=%d error=%q", res.ExitCode, res.Error)
	}
}
