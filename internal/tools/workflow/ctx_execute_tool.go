package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	core "supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

// CtxExecuteTool is the `ctx_execute` tool. It runs a
// single command in the F10 context-mode sandbox and
// returns the bounded stdout/stderr as JSON. The model
// uses it instead of `file_read` for any large file
// (logs, JSON, CSVs, source trees): it writes a small
// script or an installed project command and the
// sandbox returns just the answer.
//
// Always-on: the model sees it from turn 1 because the
// token savings apply to most "look at a big file"
// tasks. The schema is intentionally small: a 4 KB
// command ceiling, capped output, hard timeout.
type CtxExecuteTool struct {
	Runner *ctxexec.Runner
	Home   string
	// NativeOfficeOnly is enabled by the NestCafe profile. It prevents a model
	// from bypassing the native DOCX tools with ad-hoc interpreter scripts.
	NativeOfficeOnly bool
}

// NewCtxExecuteTool returns a CtxExecuteTool bound to a
// runner. Runner is required.
func NewCtxExecuteTool(runner *ctxexec.Runner, home string) *CtxExecuteTool {
	return &CtxExecuteTool{Runner: runner, Home: home}
}

// Spec returns the Tool descriptor. Output caps are clamped by the runner,
// not rejected by schema validation: an oversized preview request must not
// turn an otherwise valid command into a repair round.
func (c *CtxExecuteTool) Spec() Tool {
	return Tool{
		Name:        "ctx_execute",
		Description: "Run one sandbox command; bounded output JSON: {stdout, stderr, exit_code, truncated_stdout, truncated_stderr, duration_ms, command, workdir, error}. Never read/create/edit/convert/unpack DOCX here; use read_docx/edit_docx. Set timeouts for long builds/tests (default 10s, max 5min).",
		Schema: fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"command": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 32, "description": "argv (binary + args), not shell text; e.g. [\"git\",\"status\",\"--short\"]. Only installed PATH binaries, resolved directly. JSON-encoded argv strings also work (\"[\\\"git\\\",\\\"status\\\"]\"). Use search_code rather than assuming rg exists. Windows built-ins: [\"cmd\",\"/c\",...]."},
				"workdir": {"type": "string", "description": "Home/workspace-relative working dir; default: home root."},
				"timeout_ms": {"type": "integer", "minimum": 100, "maximum": %d, "default": 10000, "description": "Timeout (ms)."},
				"max_stdout_kb": {"type": "integer", "minimum": 1, "default": 16, "description": "stdout cap in KB, clamped to 64; keeps tail."},
				"max_stderr_kb": {"type": "integer", "minimum": 1, "default": 4, "description": "stderr cap in KB, clamped to 64; keeps tail."},
				"env_extra": {"type": "array", "items": {"type": "string"}, "description": "Optional KEY=VALUE env vars. Rarely needed."}
			},
			"required": ["command"]
		}`, ctxexec.MaxTimeoutMSHard),
		Fn: c.Execute,
	}
}

type ctxExecParams struct {
	Command     []string `json:"command"`
	Workdir     string   `json:"workdir,omitempty"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
	MaxStdoutKB int      `json:"max_stdout_kb,omitempty"`
	MaxStderrKB int      `json:"max_stderr_kb,omitempty"`
	EnvExtra    []string `json:"env_extra,omitempty"`
}

func (p *ctxExecParams) Validate() error {
	if len(p.Command) == 0 {
		return fmt.Errorf("ctx_execute: command is required")
	}
	for i, a := range p.Command {
		if a == "" {
			return fmt.Errorf("ctx_execute: command[%d] is empty", i)
		}
	}
	return nil
}

// Execute runs the request and returns the JSON result
// as Text (the model sees the JSON string, parses it
// if needed). Errors set both Result.Err and the
// second return.
func (c *CtxExecuteTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if c.Runner == nil {
		return Result{Err: errors.New("ctx_execute: runner not configured")},
			errors.New("ctx_execute: runner not configured")
	}
	var p ctxExecParams
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("ctx_execute: bad args: %w", err)},
			fmt.Errorf("ctx_execute: bad args: %w", err)
	}
	if err := p.Validate(); err != nil {
		return Result{Err: err}, err
	}
	if c.NativeOfficeOnly && c.isOfficeScript(p) {
		err := errors.New("ctx_execute: DOCX scripting is disabled in NestCafe. Use read_docx once, then edit_docx action=batch with all Word changes; no command was run")
		return Result{Err: err}, nil
	}
	req := &ctxexec.Request{
		Command:     p.Command,
		Workdir:     p.Workdir,
		TimeoutMS:   p.TimeoutMS,
		MaxStdoutKB: p.MaxStdoutKB,
		MaxStderrKB: p.MaxStderrKB,
		EnvExtra:    p.EnvExtra,
	}
	res, runErr := c.Runner.Run(ctx, req)
	if res == nil {
		res = &ctxexec.Result{ExitCode: ctxexec.ExitSandboxError}
	}
	// Marshal the result to JSON so the model sees a
	// structured payload (and can be re-fed it
	// verbatim if it wants to inspect details).
	jb, _ := json.Marshal(res)
	freshJSON := ctxExecuteFreshJSON(string(jb))
	result := Result{Text: string(freshJSON), RetainedText: res.RetainedJSON()}
	if runErr != nil {
		result.Err = runErr
		return result, runErr
	}
	// If the underlying run failed (non-zero exit),
	// surface the structured failure summary in Err —
	// first line "command_failed exit=N (D)" plus the
	// bounded stderr/stdout evidence — so the model can
	// self-correct in one turn. The JSON text is still
	// returned for UIs that show tool output; the error
	// is marked self-contained so Result.ModelContent
	// does not append the JSON (same streams) a second
	// time for the model.
	if res.ExitCode != 0 {
		failure := errors.New(res.FailureSummary())
		// Preserve caller cancellation for the loop, which records an interrupted
		// operation without teaching the repeated-failure gate a false error.
		// A command reaching its own timeout remains an ordinary tool failure.
		if cause := ctx.Err(); cause != nil && (res.ExitCode == ctxexec.ExitTimeout || res.Error == cause.Error()) {
			failure = fmt.Errorf("%s: %w", res.FailureSummary(), cause)
		}
		result.Err = core.SelfContainedErr(failure)
		return result, nil
	}
	if len(result.Text) > core.ModelOutputInlineBytes {
		result.ModelPreview = res.SuccessPreviewFromJSON(jb)
	} else {
		result.ModelText = ctxExecuteInlineModelText(res, p.Command, result)
		if result.ModelText == "" {
			result.ModelText = ctxExecuteFreshShortCommandModelText(freshJSON, res, p.Command, result)
		}
	}
	return result, nil
}

// Only Execute's own typed marshal produces this marker. It is never a view of
// persisted history, provider payloads or arbitrary custom tool Result.Text.
type ctxExecuteFreshJSON string

const (
	ctxExecuteShortCommandMinBytes = 64
	ctxExecuteShortResultMaxBytes  = 1024
)

// Small fresh results can reuse their paired argv without encoding output again.
// The original Text remains byte-identical; only the model's equivalent view is
// copied. Cap that extra string to 1 KiB and preserve existing legacy projections.
func ctxExecuteFreshShortCommandModelText(fresh ctxExecuteFreshJSON, res *ctxexec.Result, command []string, result Result) string {
	if res == nil || result.Err != nil || res.ExitCode != 0 || res.Error != "" ||
		result.RetainedText != "" || result.ModelPreview != "" || result.ModelText != "" ||
		len(fresh) > ctxExecuteShortResultMaxBytes || result.Text != string(fresh) ||
		len(res.Command) < ctxExecuteShortCommandMinBytes || len(res.Command) >= ctxExecuteDuplicateCommandMinBytes ||
		!ctxExecuteExactCommand(res.Command, command) {
		return ""
	}
	text := string(fresh)
	const marker = `,"command":`
	start := strings.Index(text, marker)
	if start < 0 || strings.Index(text[start+len(marker):], marker) >= 0 {
		return ""
	}
	valueStart := start + len(marker)
	if valueStart >= len(text) || text[valueStart] != '"' {
		return ""
	}
	// This known, freshly encoded flat struct has a string command followed by
	// workdir. Its JSON quotes/escapes are bounded by six bytes per input byte.
	// Escaped marker text inside stdout/stderr cannot match the field marker.
	limit := valueStart + 2 + 6*len(res.Command)
	if limit > len(text) {
		limit = len(text)
	}
	for end := valueStart + 1; end < limit; end++ {
		switch text[end] {
		case '\\':
			end++
		case '"':
			end++
			if end >= len(text) || text[end] != ',' {
				return ""
			}
			var model strings.Builder
			model.Grow(len(text) - (end - start))
			model.WriteString(text[:start])
			model.WriteString(text[end:])
			return model.String()
		}
	}
	return ""
}

// Check strings.Join(argv, " ") equality without allocating the joined copy.
func ctxExecuteExactCommand(actual string, argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	pos := 0
	for i, arg := range argv {
		if i != 0 {
			if pos >= len(actual) || actual[pos] != ' ' {
				return false
			}
			pos++
		}
		if len(arg) > len(actual)-pos || actual[pos:pos+len(arg)] != arg {
			return false
		}
		pos += len(arg)
	}
	return pos == len(actual)
}

// Re-encoding output is reserved for longer echoed scripts. Smaller results can
// use Execute's fresh typed JSON path above without another encoding.
const ctxExecuteDuplicateCommandMinBytes = 256

// ctxExecuteInlineModelText removes a byte-identical long command echo or JSON
// HTML escapes from a small successful result. Values, capture diagnostics and
// unmatched commands stay complete. Text remains the original live UI evidence.
func ctxExecuteInlineModelText(res *ctxexec.Result, command []string, result Result) string {
	if res == nil || result.Err != nil || res.ExitCode != 0 || res.Error != "" ||
		result.RetainedText != "" || result.ModelPreview != "" ||
		len(result.Text) > core.ModelOutputInlineBytes {
		return ""
	}
	duplicateCommand := len(res.Command) >= ctxExecuteDuplicateCommandMinBytes &&
		res.Command == strings.Join(command, " ")
	// JSON here is tool text, not HTML. Avoid another encoding unless the escaped
	// characters offer a useful reduction; ordinary command results keep the fast path.
	const minHTMLGain = 512
	htmlCandidate := len(result.Text) >= minHTMLGain &&
		5*(strings.Count(result.Text, "\\u003c")+
			strings.Count(result.Text, "\\u003e")+
			strings.Count(result.Text, "\\u0026")) >= minHTMLGain
	if !duplicateCommand && !htmlCandidate {
		return ""
	}
	var view any = res
	if duplicateCommand {
		view = struct {
			*ctxexec.Result
			Command *string `json:"command,omitempty"`
		}{Result: res}
	}
	if !htmlCandidate {
		data, err := json.Marshal(view)
		if err != nil {
			return ""
		}
		return string(data)
	}
	var buffer bytes.Buffer
	buffer.Grow(len(result.Text))
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(view); err != nil {
		return ""
	}
	data := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	// Literal backslash-u text can resemble an HTML escape in the cheap scan.
	// Validate the actual gain so those results retain the original representation.
	if !duplicateCommand && len(result.Text)-len(data) < minHTMLGain {
		return ""
	}
	return string(data)
}

var officeScriptMarkers = []string{
	".docx", "python-docx", "from docx", "import docx", "wordprocessingml",
	"documentformat.openxml", "word.application", "winword", "document.xml",
	"[content_types].xml", "officeopenxml",
}

func (c *CtxExecuteTool) isOfficeScript(p ctxExecParams) bool {
	if len(p.Command) == 0 || !isScriptInterpreter(p.Command[0]) {
		return false
	}
	if containsOfficeScriptMarker(strings.Join(p.Command[1:], "\n")) {
		return true
	}
	// Catch the common two-step workaround: create helper.py/helper.ps1, then
	// execute only its filename. Read solely local, bounded script sources.
	base := c.Home
	if strings.TrimSpace(p.Workdir) != "" {
		base = filepath.Join(base, p.Workdir)
	}
	for _, arg := range p.Command[1:] {
		ext := strings.ToLower(filepath.Ext(strings.Trim(arg, "\"'")))
		if ext != ".py" && ext != ".ps1" && ext != ".js" && ext != ".mjs" && ext != ".cjs" {
			continue
		}
		path := strings.Trim(arg, "\"'")
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		data, err := os.ReadFile(path)
		if err == nil && len(data) <= 512*1024 && containsOfficeScriptMarker(string(data)) {
			return true
		}
	}
	return false
}

func isScriptInterpreter(command string) bool {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "python", "python3", "py", "powershell", "pwsh", "cmd", "sh", "bash", "node", "deno", "bun":
		return true
	default:
		return false
	}
}

func containsOfficeScriptMarker(s string) bool {
	s = strings.ToLower(s)
	for _, marker := range officeScriptMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// formatCtxExecForTUI is a tiny pretty-printer used by
// the TUI for /ctx invocations. It is NOT used for the
// tool path (which returns raw JSON to the model).
func formatCtxExecForTUI(res *ctxexec.Result) string {
	if res == nil {
		return "(nil result)"
	}
	var b strings.Builder
	if res.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", res.Error)
	}
	if res.Workdir != "" {
		fmt.Fprintf(&b, "workdir: %s\n", res.Workdir)
	}
	fmt.Fprintf(&b, "exit: %d  duration: %dms\n", res.ExitCode, res.DurationMS)
	if res.TruncatedStdout {
		b.WriteString("[stdout truncated to cap]\n")
	}
	if res.TruncatedStderr {
		b.WriteString("[stderr truncated to cap]\n")
	}
	if res.Stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(res.Stdout)
		if !strings.HasSuffix(res.Stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if res.Stderr != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(res.Stderr)
		if !strings.HasSuffix(res.Stderr, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
