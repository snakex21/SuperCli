package ctxexec

import (
	"bytes"
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
	stdout, _ := json.Marshal(r.Stdout)
	stderr, _ := json.Marshal(r.Stderr)
	return r.successPreview(stdout, stderr)
}

// SuccessPreviewFromJSON reuses stream strings from json.Marshal(r) immediately
// produced by the caller. The input is owned encoding output, never untrusted
// tool/model JSON. No slices outlive the bounded preview built here. Noncanonical
// field layouts fall back to the ordinary encoder.
func (r *Result) SuccessPreviewFromJSON(encoded []byte) string {
	if r == nil || r.ExitCode != 0 || r.Error != "" {
		return ""
	}
	stdout, stderr, ok := encodedPreviewStreams(encoded)
	if !ok {
		return r.SuccessPreview()
	}
	return r.successPreview(stdout, stderr)
}

func (r *Result) successPreview(stdout, stderr json.RawMessage) string {
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

// Scan only fixed leading fields emitted by Result, respecting JSON escapes.
// This is not a JSON validator: json.Marshal owns validity at the call site.
func encodedPreviewStreams(raw []byte) (json.RawMessage, json.RawMessage, bool) {
	const prefix = "{\"stdout\":"
	const middle = ",\"stderr\":"
	if !bytes.HasPrefix(raw, []byte(prefix)) {
		return nil, nil, false
	}
	end := encodedStringEnd(raw, len(prefix))
	if end < 0 || !bytes.HasPrefix(raw[end:], []byte(middle)) {
		return nil, nil, false
	}
	start := end + len(middle)
	end2 := encodedStringEnd(raw, start)
	if end2 < 0 || !bytes.HasPrefix(raw[end2:], []byte(",\"exit_code\":")) {
		return nil, nil, false
	}
	return raw[len(prefix):end], raw[start:end2], true
}

func encodedStringEnd(raw []byte, start int) int {
	if start >= len(raw) || raw[start] != '"' {
		return -1
	}
	for i := start + 1; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}
