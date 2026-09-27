package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

type workerCommandView struct {
	ID   string
	Text string
}

// Evaluation only: change the presentation at the provider boundary, after
// execution and verification. Keep every captured byte and execution field.
func workerCommandText(content string) string {
	if len(content) > 8192 {
		return content
	}
	var result struct {
		Stdout          string `json:"stdout"`
		Stderr          string `json:"stderr"`
		ExitCode        *int   `json:"exit_code"`
		TruncatedStdout bool   `json:"truncated_stdout"`
		TruncatedStderr bool   `json:"truncated_stderr"`
		DurationMS      int64  `json:"duration_ms"`
		Command         string `json:"command"`
		Workdir         string `json:"workdir"`
		Error           string `json:"error"`
	}
	if json.Unmarshal([]byte(content), &result) != nil || result.ExitCode == nil || *result.ExitCode != 0 || result.Error != "" || result.TruncatedStdout || result.TruncatedStderr {
		return content
	}
	var out strings.Builder
	fmt.Fprintf(&out, "command_completed exit_code=0 duration_ms=%d\ncommand: %s\nworkdir: %s", result.DurationMS, result.Command, result.Workdir)
	for _, stream := range []struct{ name, text string }{{"stdout", result.Stdout}, {"stderr", result.Stderr}} {
		if stream.text != "" {
			fmt.Fprintf(&out, "\n%s:\n%s", stream.name, stream.text)
		}
	}
	return out.String()
}

func workerCommandRequest(messages []llm.Message, plain bool) ([]llm.Message, []workerCommandView) {
	var views []workerCommandView
	var copied bool
	for i, message := range messages {
		if message.Role != llm.RoleTool || message.Name != "ctx_execute" {
			continue
		}
		text := message.Content
		if plain {
			text = workerCommandText(text)
		}
		if text != message.Content {
			if !copied {
				messages = append([]llm.Message(nil), messages...)
				copied = true
			}
			messages[i].Content = text
		}
		views = append(views, workerCommandView{ID: message.ToolCallID, Text: text})
	}
	return messages, views
}

func TestWorkerCommandPresentationExperiment(t *testing.T) {
	original := `{"stdout":"PASS\nok\tfixture\n","stderr":"warning\n","exit_code":0,"duration_ms":123,"command":"go test ./...","workdir":"C:\\fixture","truncated_stdout":false,"truncated_stderr":false}`
	messages := []llm.Message{{Role: llm.RoleTool, Name: "ctx_execute", ToolCallID: "check-1", Content: original}, {Role: llm.RoleTool, Name: "custom", Content: original}}
	changed, views := workerCommandRequest(messages, true)
	if messages[0].Content != original || changed[1].Content != original || len(views) != 1 || views[0].ID != "check-1" {
		t.Fatal("experiment changed unrelated data or original history")
	}
	for _, want := range []string{"exit_code=0", "duration_ms=123", "go test ./...", `C:\fixture`, "stdout:\nPASS\nok\tfixture\n", "stderr:\nwarning\n"} {
		if !strings.Contains(changed[0].Content, want) {
			t.Fatalf("lost %q", want)
		}
	}
	baseline, _ := workerCommandRequest(messages, false)
	if baseline[0].Content != original {
		t.Fatal("baseline altered")
	}
	for _, input := range []string{`{"stdout":"PASS"}`, `{"exit_code":1,"stdout":"FAIL"}`, `{"exit_code":0,"error":"sandbox failure"}`, `{"exit_code":0,"truncated_stdout":true}`, `{"exit_code":0,"truncated_stderr":true}`, "error: command_failed exit=1", strings.Repeat("x", 9000)} {
		if workerCommandText(input) != input {
			t.Fatalf("unsafe rewrite of %q", input[:min(40, len(input))])
		}
	}
}
