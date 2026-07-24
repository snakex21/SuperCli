package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

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
	stream, err := c.Verdict.Complete(llm.WithPurpose(ctx, llm.PurposeVerdict), msgs, nil)
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
