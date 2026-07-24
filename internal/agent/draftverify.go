package agent

import (
	"context"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/system/childproc"
)

// DraftVerifyConfig configures the draft-verify ladder (design 2026-07-05/06).
// The zero value is OFF: task delegation behaves byte-identically to before.
// When Enabled is true and a worker touched files, the objective sieve runs
// the configured commands, then the coordinator's (big) model issues a verdict
// on the DIFF and the sieve EVIDENCE — never on the worker's narration.
type DraftVerifyConfig struct {
	// Enabled turns the whole ladder on. OFF (default) = no sieve, no
	// verdict, no extra cost; the task tool returns the worker report as-is.
	Enabled bool

	// VerifyCommands is the objective sieve: each entry is a full command
	// line (e.g. "go build ./...", "go test ./..."). They run in order in
	// the sandbox root; the first non-zero exit stops the sieve and its
	// output becomes the RED evidence. Empty = the sieve is skipped and the
	// verdict runs on the diff alone (still useful, just weaker evidence).
	VerifyCommands []string

	// MaxRounds is the hard cap on REVISE round-trips before the big model
	// takes over (or the best draft is returned annotated). 0 = default 2.
	MaxRounds int

	// SieveTimeout bounds ONE verify command. 0 = default 120s. The whole
	// sieve is bounded by len(commands) * SieveTimeout.
	SieveTimeout time.Duration

	// Verdict is the LLM backend that issues the ACCEPT/REVISE/TAKEOVER
	// call. This is the coordinator's (big) model. Nil disables the LLM
	// verdict: a green sieve then means ACCEPT, a red sieve means the draft
	// is returned annotated (no takeover — there is no big model to take
	// over with).
	Verdict llm.Provider

	// runCommand is the sieve executor, overridable in tests. Nil uses the
	// real exec-based runner. It returns exit code + a truncated tail of
	// combined output.
	runCommand func(ctx context.Context, dir, command string, timeout time.Duration) (exit int, output string)

	// gitDiff gathers the change evidence, overridable in tests. Nil uses
	// `git diff` (plus untracked files) in dir.
	gitDiff func(ctx context.Context, dir string) string
}

// draftVerifyMaxEvidenceBytes caps every evidence blob (sieve output, diff)
// fed to the verdict model, mirroring the tool-result truncation policy: keep
// the freshest tail, mark the cut.
const draftVerifyMaxEvidenceBytes = 6000

// sieveResult is the objective sieve outcome: which command decided it, its
// exit code, and the truncated output tail. Green is true when every command
// exited 0 (or there were no commands).
type sieveResult struct {
	Green   bool
	Command string
	Exit    int
	Output  string
	Skipped bool // no commands configured
}

// runSieve executes the verify commands in order and stops at the first red.
func (c *DraftVerifyConfig) runSieve(ctx context.Context, dir string) sieveResult {
	if len(c.VerifyCommands) == 0 {
		return sieveResult{Green: true, Skipped: true}
	}
	timeout := c.SieveTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	run := c.runCommand
	if run == nil {
		run = runSieveCommand
	}
	for _, cmd := range c.VerifyCommands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		exit, out := run(ctx, dir, cmd, timeout)
		if exit != 0 {
			return sieveResult{
				Green:   false,
				Command: cmd,
				Exit:    exit,
				Output:  clampEvidence(out),
			}
		}
	}
	return sieveResult{Green: true}
}

// runSieveCommand executes one command line in dir with a timeout, returning
// the exit code and a truncated tail of stdout+stderr. It splits the command
// on whitespace (no shell) so the sieve stays predictable and cross-platform.
// A missing binary or spawn failure is reported as a non-zero exit so the
// sieve treats it as RED rather than silently passing.
func runSieveCommand(ctx context.Context, dir, command string, timeout time.Duration) (int, string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return 0, ""
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, fields[0], fields[1:]...)
	childproc.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := string(out)
	if runCtx.Err() == context.DeadlineExceeded {
		return 124, text + "\n[sieve: command timed out]"
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code := ee.ExitCode()
			if code == 0 {
				code = 1
			}
			return code, text
		}
		// Spawn failure (binary not found, etc.) — RED, so a misconfigured
		// sieve never masquerades as a passing build.
		return 127, text + "\n[sieve: " + err.Error() + "]"
	}
	return 0, text
}

// gatherDiff returns the change evidence for the verdict: `git diff` of tracked
// files plus a listing of untracked files. Empty when the dir is not a git repo
// or nothing changed.
func (c *DraftVerifyConfig) gatherDiff(ctx context.Context, dir string) string {
	if c.gitDiff != nil {
		return clampEvidence(c.gitDiff(ctx, dir))
	}
	return clampEvidence(defaultGitDiff(ctx, dir))
}

func defaultGitDiff(ctx context.Context, dir string) string {
	diff := gitCapture(ctx, dir, "diff")
	untracked := gitCapture(ctx, dir, "ls-files", "--others", "--exclude-standard")
	var b strings.Builder
	b.WriteString(strings.TrimSpace(diff))
	if u := strings.TrimSpace(untracked); u != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("new (untracked) files:\n" + u)
	}
	return b.String()
}

func gitCapture(ctx context.Context, dir string, args ...string) string {
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	childproc.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// clampEvidence truncates an evidence blob to the freshest tail, matching the
// tool-result truncation policy (keep the end, mark the cut).
func clampEvidence(s string) string {
	if len(s) <= draftVerifyMaxEvidenceBytes {
		return s
	}
	// Move the cut forward (at most 3 bytes) to the next rune
	// boundary so a multi-byte character is never split in half.
	i := len(s) - draftVerifyMaxEvidenceBytes
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "[...truncated...]\n" + s[i:]
}

// verdictKind is the parsed decision from the big model.
