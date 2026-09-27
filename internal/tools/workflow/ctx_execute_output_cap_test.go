package workflow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"supercli/internal/tools/ctxexec"
)

func TestCtxOutputCapHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_OUTPUT_CAP_HELPER") != "1" {
		return
	}
	for _, stream := range []*os.File{os.Stdout, os.Stderr} {
		if _, err := stream.WriteString(strings.Repeat("x", 96*1024) + "END\n"); err != nil {
			os.Exit(2)
		}
	}
	os.Exit(0)
}

// Exercise the registry too: the runner already clamped oversized caps, but
// schema validation used to reject them before the command could execute.
func TestCtxExecuteRegistryClampsOutputCaps(t *testing.T) {
	for _, tc := range []struct {
		name             string
		stdout, stderr   int
		wantOut, wantErr int
	}{
		{"hard limit", 64, 64, 64, 64},
		{"oversized request from session", 3000, 3000, 64, 64},
		{"small stdout unchanged", 1, 3000, 1, 64},
		{"small stderr unchanged", 3000, 1, 64, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			registry := NewRegistry()
			registry.MustRegister(NewCtxExecuteTool(ctxexec.New(root), root).Spec())
			args, err := json.Marshal(map[string]any{
				"command":       []string{os.Args[0], "-test.run=^TestCtxOutputCapHelper$"},
				"env_extra":     []string{"SUPERCLI_OUTPUT_CAP_HELPER=1"},
				"max_stdout_kb": tc.stdout,
				"max_stderr_kb": tc.stderr,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := registry.Execute(context.Background(), "ctx_execute", args)
			if err != nil || result.Err != nil {
				t.Fatalf("command rejected: %v / %v", err, result.Err)
			}
			var output ctxexec.Result
			if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
				t.Fatal(err)
			}
			if output.ExitCode != 0 || !output.TruncatedStdout || !output.TruncatedStderr {
				t.Fatalf("unexpected result: exit=%d, truncated=%v/%v", output.ExitCode, output.TruncatedStdout, output.TruncatedStderr)
			}
			if len(output.Stdout) > tc.wantOut*1024 || len(output.Stderr) > tc.wantErr*1024 {
				t.Fatalf("output exceeded cap: %d/%d bytes", len(output.Stdout), len(output.Stderr))
			}
			for _, stream := range []string{output.Stdout, output.Stderr} {
				if !strings.HasSuffix(stream, "END\n") {
					t.Fatal("missing end of command output")
				}
			}
		})
	}
}
