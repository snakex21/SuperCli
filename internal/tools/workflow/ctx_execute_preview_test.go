package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

func TestCommandPreviewHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_COMMAND_PREVIEW_HELPER")
	if mode == "" {
		return
	}
	if mode == "small" {
		fmt.Println("small result")
		os.Exit(0)
	}
	fmt.Fprint(os.Stdout, "STDOUT_BEGIN\n", strings.Repeat("output line\n", 550), "MIDDLE_DETAIL\n", strings.Repeat("output line\n", 550), "STDOUT_END\n")
	fmt.Fprint(os.Stderr, "STDERR_BEGIN\n", strings.Repeat("warning line\n", 550), "STDERR_END\n")
	if mode == "failure" {
		os.Exit(7)
	}
	os.Exit(0)
}

func TestCtxExecuteStructuredPreviewPreservesBothStreams(t *testing.T) {
	for _, mode := range []string{"small", "large", "retained", "failure"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			tool := NewCtxExecuteTool(ctxexec.New(home), home)
			command := []string{os.Args[0], "-test.run=^TestCommandPreviewHelper$"}
			if mode != "small" {
				command = append(command, "--", strings.Repeat("x", 3500))
			}
			stdoutCap := 16
			if mode == "retained" {
				stdoutCap = 1
			}
			args, _ := json.Marshal(map[string]any{"command": command, "max_stdout_kb": stdoutCap, "max_stderr_kb": 16, "env_extra": []string{"SUPERCLI_COMMAND_PREVIEW_HELPER=" + mode}})
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			store := core.NewOutputStore()
			content := store.ModelContent("ctx_execute", result)
			if mode == "small" {
				if result.ModelPreview != "" || content != result.Text {
					t.Fatal("small result changed")
				}
				return
			}
			if mode == "failure" {
				if result.ModelPreview != "" || !strings.HasPrefix(content, "error: command_failed exit=7") {
					t.Fatal("failure summary changed")
				}
				return
			}
			body, _, ok := strings.Cut(content, "\n[stored tool output: ")
			var preview struct {
				Stdout          string `json:"stdout"`
				Stderr          string `json:"stderr"`
				ExitCode        *int   `json:"exit_code"`
				TruncatedStdout bool   `json:"truncated_stdout"`
				TruncatedStderr bool   `json:"truncated_stderr"`
			}
			if !ok || json.Unmarshal([]byte(body), &preview) != nil {
				t.Fatal("preview is not intact JSON with a retained reference")
			}
			if preview.ExitCode == nil || *preview.ExitCode != 0 || !preview.TruncatedStdout || !preview.TruncatedStderr {
				t.Fatal("preview outcome or truncation flags lost")
			}
			markers := []string{"STDOUT_END", "STDERR_BEGIN", "STDERR_END"}
			if mode != "retained" {
				markers = append(markers, "STDOUT_BEGIN")
			}
			for _, marker := range markers {
				if !strings.Contains(body, marker) {
					t.Fatalf("stream evidence lost: %s", marker)
				}
			}
			if len(result.ModelPreview) > core.ModelOutputPreviewBytes {
				t.Fatalf("preview exceeds budget: %d", len(result.ModelPreview))
			}
			var original ctxexec.Result
			if err := json.Unmarshal([]byte(result.Text), &original); err != nil {
				t.Fatal(err)
			}
			if original.TruncatedStdout != (mode == "retained") || original.TruncatedStderr || (mode != "retained" && !strings.Contains(original.Stdout, "MIDDLE_DETAIL")) {
				t.Fatal("UI/raw result was modified")
			}
			handle := core.StoredOutputHandle(content)
			readArgs, _ := json.Marshal(map[string]any{"handle": handle, "query": "MIDDLE_DETAIL"})
			recovered, err := store.ReadOutputTool().Fn(context.Background(), readArgs)
			if err != nil || recovered.Err != nil || !strings.Contains(recovered.Text, "MIDDLE_DETAIL") {
				t.Fatal("original evidence unavailable")
			}
			generic := result
			generic.ModelPreview = ""
			old := core.NewOutputStore().ModelContent("ctx_execute", generic)
			oldFound, newFound := 0, 0
			for _, marker := range []string{"STDOUT_BEGIN", "STDOUT_END", "STDERR_BEGIN", "STDERR_END"} {
				if strings.Contains(old, marker) {
					oldFound++
				}
				if strings.Contains(content, marker) {
					newFound++
				}
			}
			t.Logf("generic=%d bytes/%d stream endpoints; structured=%d bytes/%d endpoints", len(old), oldFound, len(content), newFound)
		})
	}
}
