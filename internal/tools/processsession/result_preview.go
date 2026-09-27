package processsession

import (
	"encoding/json"

	"supercli/internal/tools/core"
)

// modelPreview keeps process identity and state outside the truncated streams.
// The original snapshot (including argv/workdir) stays in the output store.
// This is used only for successful tool results exceeding the inline limit;
// failed commands keep the shared actionable failure diagnostic instead.
func (s snapshot) modelPreview() string {
	preview := struct {
		ID              string          `json:"id"`
		Status          string          `json:"status"`
		ExitCode        *int            `json:"exit_code,omitempty"`
		DurationMS      int64           `json:"duration_ms"`
		PTY             bool            `json:"pty,omitempty"`
		Preview         bool            `json:"preview"`
		Stdout          json.RawMessage `json:"stdout"`
		Stderr          json.RawMessage `json:"stderr"`
		TruncatedStdout bool            `json:"truncated_stdout"`
		TruncatedStderr bool            `json:"truncated_stderr"`
		OmittedOut      int64           `json:"omitted_stdout_bytes,omitempty"`
		OmittedErr      int64           `json:"omitted_stderr_bytes,omitempty"`
	}{ID: s.ID, Status: s.Status, ExitCode: s.ExitCode, DurationMS: s.DurationMS, PTY: s.PTY, Preview: true, Stdout: json.RawMessage(`""`), Stderr: json.RawMessage(`""`), OmittedOut: s.OmittedOut, OmittedErr: s.OmittedErr}
	empty, _ := json.Marshal(preview)
	remaining := core.ModelOutputPreviewBytes - len(empty) + 4
	if remaining < 4 {
		return ""
	}
	stdout, _ := json.Marshal(s.Stdout)
	stderr, _ := json.Marshal(s.Stderr)
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
	preview.TruncatedStdout = s.OmittedOut > 0 || shortened[0]
	preview.TruncatedStderr = s.OmittedErr > 0 || shortened[1]
	body, _ := json.Marshal(preview)
	return string(body)
}
