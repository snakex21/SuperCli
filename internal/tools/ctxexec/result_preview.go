package ctxexec

import (
	"encoding/json"

	"supercli/internal/tools/core"
)

// SuccessPreview keeps each stream and the execution outcome within the existing
// model preview budget. The caller uses it only when the complete JSON is large;
// command/workdir remain in the tool call and the retained original result.
func (r *Result) SuccessPreview() string {
	if r == nil || r.ExitCode != 0 || r.Error != "" {
		return ""
	}
	preview := struct {
		ExitCode         int             `json:"exit_code"`
		DurationMS       int64           `json:"duration_ms"`
		Preview          bool            `json:"preview"`
		OutputWarning    string          `json:"output_warning,omitempty"`
		OutputIncomplete bool            `json:"output_incomplete,omitempty"`
		Stdout           json.RawMessage `json:"stdout"`
		Stderr           json.RawMessage `json:"stderr"`
		TruncatedStdout  bool            `json:"truncated_stdout"`
		TruncatedStderr  bool            `json:"truncated_stderr"`
	}{ExitCode: r.ExitCode, DurationMS: r.DurationMS, Preview: true, OutputWarning: r.OutputWarning, OutputIncomplete: r.OutputIncomplete, Stdout: json.RawMessage(`""`), Stderr: json.RawMessage(`""`)}
	// Empty-string quotes are part of the per-stream budget; false is one byte
	// longer than true, so subsequent truncation flags cannot overflow the cap.
	empty, _ := json.Marshal(preview)
	remaining := core.ModelOutputPreviewBytes - len(empty) + 4
	stdout, _ := json.Marshal(r.Stdout)
	stderr, _ := json.Marshal(r.Stderr)
	streams := [2]json.RawMessage{stdout, stderr}
	shortened := [2]bool{}
	first := 0
	if len(stderr) < len(stdout) {
		first = 1
	}
	for position, i := range []int{first, 1 - first} {
		streams[i], shortened[i] = core.PreviewJSONString(streams[i], remaining/(2-position))
		remaining -= len(streams[i])
	}
	preview.Stdout, preview.Stderr = streams[0], streams[1]
	preview.TruncatedStdout = r.TruncatedStdout || shortened[0]
	preview.TruncatedStderr = r.TruncatedStderr || shortened[1]
	body, _ := json.Marshal(preview)
	return string(body)
}
