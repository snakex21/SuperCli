package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"supercli/internal/ctxexec"
)

// CtxExecuteTool is the `ctx_execute` tool. It runs a
// single command in the F10 context-mode sandbox and
// returns the bounded stdout/stderr as JSON. The model
// uses it instead of `file_read` for any large file
// (logs, JSON, CSVs, source trees): it writes a small
// script (grep, awk, python3 -c, jq, ...) and the
// sandbox returns just the answer.
//
// Always-on: the model sees it from turn 1 because the
// token savings apply to most "look at a big file"
// tasks. The schema is intentionally small: a 4 KB
// command ceiling, capped output, hard timeout.
type CtxExecuteTool struct {
	Runner *ctxexec.Runner
	Home   string
}

// NewCtxExecuteTool returns a CtxExecuteTool bound to a
// runner. Runner is required.
func NewCtxExecuteTool(runner *ctxexec.Runner, home string) *CtxExecuteTool {
	return &CtxExecuteTool{Runner: runner, Home: home}
}

// Spec returns the Tool descriptor.
func (c *CtxExecuteTool) Spec() Tool {
	return Tool{
		Name: "ctx_execute",
		Description: "Run a single command in a sandboxed context-mode environment and return ONLY its bounded stdout. Use this INSTEAD of file_read for any file larger than ~50 lines, for log inspection, for counting matches, for JSON/CSV slicing, and for any time you would otherwise load a large file into the conversation. " +
			"Command is a list of args (binary + arguments), NOT a shell string. The sandbox resolves the binary via PATH (returns 127 if missing), enforces a wallclock timeout, scrubs the env (no API keys leak to the child), and caps stdout (default 16 KB) and stderr (default 4 KB). " +
			"Output is JSON: {stdout, stderr, exit_code, truncated_stdout, truncated_stderr, duration_ms, command, workdir, error}.",
		Schema: `{
			"type": "object",
			"properties": {
				"command": {
					"type": "array",
					"items": {"type": "string"},
					"minItems": 1,
					"maxItems": 32,
					"description": "Binary + arguments. e.g. [\"python3\", \"-c\", \"import json,sys; print(len(json.load(sys.stdin)))\"]. The binary is resolved via PATH. Common interpreters (python3, node, jq, awk, sed, grep, rg, head, tail, wc, cut, tr, sort, uniq, cat, find, ls) are typically available."
				},
				"workdir": {
					"type": "string",
					"description": "Working directory, relative to home. Default: home root. Must resolve inside home (sandbox-checked)."
				},
				"timeout_ms": {
					"type": "integer",
					"minimum": 100,
					"maximum": 30000,
					"default": 10000,
					"description": "Wallclock timeout in milliseconds. Default 10000, max 30000."
				},
				"max_stdout_kb": {
					"type": "integer",
					"minimum": 1,
					"maximum": 64,
					"default": 16,
					"description": "Cap on stdout in KB. Default 16, max 64. Result is truncated from the front when exceeded; truncated_stdout is set."
				},
				"max_stderr_kb": {
					"type": "integer",
					"minimum": 1,
					"maximum": 64,
					"default": 4,
					"description": "Cap on stderr in KB. Default 4, max 64."
				},
				"env_extra": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Optional extra env vars in KEY=VALUE form. Rarely needed."
				}
			},
			"required": ["command"]
		}`,
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
	if runErr != nil {
		return Result{Text: string(jb), Err: runErr}, runErr
	}
	// If the underlying run failed (non-zero exit),
	// surface a short message in Err so the model
	// gets a clear signal, but ALSO return the JSON
	// text so it can read stdout.
	if res.ExitCode != 0 {
		msg := fmt.Sprintf("ctx_execute: exit %d", res.ExitCode)
		if res.Error != "" {
			msg = "ctx_execute: " + res.Error
		}
		return Result{Text: string(jb), Err: errors.New(msg)}, nil
	}
	return Result{Text: string(jb)}, nil
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
