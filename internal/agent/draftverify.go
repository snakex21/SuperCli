package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
type verdictKind int

const (
	verdictAccept verdictKind = iota
	verdictRevise
	verdictTakeover
)

func (k verdictKind) String() string {
	switch k {
	case verdictAccept:
		return "ACCEPT"
	case verdictRevise:
		return "REVISE"
	default:
		return "TAKEOVER"
	}
}

// verdict is the parsed big-model decision. Instruction is only meaningful for
// REVISE (the concrete "fix X in file Y" sent back to the worker).
type verdict struct {
	Kind        verdictKind
	Instruction string
}

// parseVerdict extracts a verdict from the model's raw output. It is
// deliberately forgiving (CoerceArgs philosophy): it tries strict JSON first,
// then a loose scan for the decision keyword, and on TOTAL failure returns a
// SAFE fallback (TAKEOVER when a big model exists, else the caller annotates)
// rather than crashing on garbage. ok=false signals the fallback path.
func parseVerdict(raw string) (verdict, bool) {
	raw = stripCodeFence(strings.TrimSpace(raw))
	if raw == "" {
		return verdict{Kind: verdictTakeover}, false
	}
	// Try strict JSON: {"decision":"revise","instruction":"..."}.
	if obj := firstJSONObject(raw); obj != "" {
		var v struct {
			Decision    string `json:"decision"`
			Instruction string `json:"instruction"`
		}
		if json.Unmarshal([]byte(obj), &v) == nil && v.Decision != "" {
			if k, ok := decisionKeyword(v.Decision); ok {
				return verdict{Kind: k, Instruction: strings.TrimSpace(v.Instruction)}, true
			}
		}
	}
	// Loose scan: first decision keyword wins; instruction = the rest.
	if k, ok := decisionKeyword(raw); ok {
		return verdict{Kind: k, Instruction: extractInstruction(raw)}, true
	}
	return verdict{Kind: verdictTakeover}, false
}

func decisionKeyword(s string) (verdictKind, bool) {
	low := strings.ToLower(s)
	// Order matters: check the whole-string keywords first.
	switch {
	case strings.Contains(low, "takeover"), strings.Contains(low, "take over"), strings.Contains(low, "take_over"):
		return verdictTakeover, true
	case strings.Contains(low, "revise"), strings.Contains(low, "reject"):
		return verdictRevise, true
	case strings.Contains(low, "accept"), strings.Contains(low, "approve"):
		return verdictAccept, true
	}
	return verdictAccept, false
}

// extractInstruction pulls a human instruction out of a loose verdict string:
// the text after an "instruction:" label, else everything after the first
// decision keyword.
func extractInstruction(s string) string {
	low := strings.ToLower(s)
	if i := strings.Index(low, "instruction"); i >= 0 {
		rest := s[i+len("instruction"):]
		rest = strings.TrimLeft(rest, " :=\"\n\t")
		return strings.TrimSpace(strings.Trim(rest, "\"}"))
	}
	return strings.TrimSpace(s)
}

func firstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line and the trailing fence.
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// verdictSystemPrompt steers the big model toward a parseable, evidence-bound
// decision. It is small (a few dozen tokens) and only sent on the verdict call.
const verdictSystemPrompt = `You are a strict code-change reviewer. A small worker model drafted a change; you decide its fate based ONLY on the diff and the objective build/test evidence below — NOT on any claim of success. The worker can report success while failing.

Reply with a single JSON object and nothing else:
{"decision":"accept|revise|takeover","instruction":"..."}
- "accept": the diff is correct and the evidence is green.
- "revise": fixable with a concrete instruction. Put the exact fix ("change X in file Y") in "instruction". Also revise a diff that overreaches the task (formatting-only or unrelated edits): tell the worker to drop them.
- "takeover": the draft is a dead end; you will redo it yourself.
Leave "instruction" empty unless the decision is "revise".`

// requestVerdict asks the big model for a decision on the diff + evidence in a
// SINGLE turn. On any transport error or empty output it returns a safe
// TAKEOVER fallback with ok=false, so a flaky verdict call never crashes or
// silently accepts. The returned Usage reports the verdict call's token cost
// (zero when the provider does not report usage) for the economics line.
func (c *DraftVerifyConfig) requestVerdict(ctx context.Context, task, expect, diff string, sieve sieveResult) (verdict, bool, llm.Usage) {
	var usage llm.Usage
	if c.Verdict == nil {
		return verdict{Kind: verdictTakeover}, false, usage
	}
	payload := buildVerdictPayload(task, expect, diff, sieve)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: verdictSystemPrompt},
		{Role: llm.RoleUser, Content: payload},
	}
	stream, err := c.Verdict.Complete(ctx, msgs, nil)
	if err != nil {
		return verdict{Kind: verdictTakeover}, false, usage
	}
	var text strings.Builder
	for d := range stream {
		if d.Err != nil {
			return verdict{Kind: verdictTakeover}, false, usage
		}
		text.WriteString(d.Content)
		if d.Usage != nil {
			usage = *d.Usage
		}
	}
	v, ok := parseVerdict(text.String())
	return v, ok, usage
}

// buildVerdictPayload assembles the concise evidence bundle for the verdict.
func buildVerdictPayload(task, expect, diff string, sieve sieveResult) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(strings.TrimSpace(task))
	if e := strings.TrimSpace(expect); e != "" {
		b.WriteString("\n\n## Required in the result\n")
		b.WriteString(e)
	}
	b.WriteString("\n\n## Objective sieve (build/test)\n")
	switch {
	case sieve.Skipped:
		b.WriteString("(no verify commands configured; judge on the diff alone)")
	case sieve.Green:
		b.WriteString("GREEN — all verify commands exited 0.")
	default:
		fmt.Fprintf(&b, "RED — `%s` exited %d:\n%s", sieve.Command, sieve.Exit, sieve.Output)
	}
	b.WriteString("\n\n## Diff of changed files\n")
	if strings.TrimSpace(diff) == "" {
		b.WriteString("(no tracked changes detected)")
	} else {
		b.WriteString(diff)
	}
	return b.String()
}

// draftVerifyTelemetry is the one-line economics record: what the ladder cost
// so the user can MEASURE whether draft-verify pays off (it is not lossless).
type draftVerifyTelemetry struct {
	Rounds       int
	Outcome      string // ACCEPT / TAKEOVER / annotated / disabled
	DraftSteps   int
	DraftTokIn   int
	DraftTokOut  int
	VerifyTokIn  int
	VerifyTokOut int
	SieveRed     int // number of rounds the sieve was red
}

// Line renders the economics as a single, greppable telemetry line.
func (t draftVerifyTelemetry) Line() string {
	return fmt.Sprintf(
		"draft-verify: %s · %d round(s) · draft %d steps %d/%d tok · verify %d/%d tok · sieve %d red",
		t.Outcome, t.Rounds, t.DraftSteps, t.DraftTokIn, t.DraftTokOut,
		t.VerifyTokIn, t.VerifyTokOut, t.SieveRed)
}
