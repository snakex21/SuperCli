package darwin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"supercli/internal/llm"
)

// Judge picks the best Candidate from a Darwin run.
// Implementations are expected to be deterministic
// for a given input (modulo LLM non-determinism).
type Judge interface {
	// Judge returns a Verdict or an error. The
	// judge receives the full set of candidates
	// and may inspect Text, Err, Steps, Usage.
	Judge(ctx context.Context, prompt string, cands []Candidate) (Verdict, error)
}

// Verdict is the judge's pick plus a short
// explanation. Score is in [0, 1]; 0 means the
// judge is undecided (CompositeJudge falls back
// to the heuristic in that case).
type Verdict struct {
	WinnerIndex int
	Score       float64
	Reason      string
}

// HeuristicJudge scores candidates by simple
// signals that don't require an LLM call:
//   - candidates with Err are penalised
//   - candidates that mention "test", "spec",
//     "lint", "fmt", or "vet" as whole words in
//     their text are rewarded (word-boundary
//     matching so "text" doesn't trigger "test")
//   - candidates with very short text are
//     penalised
//   - candidates that are too verbose (> 4000
//     chars) are penalised
//   - candidates with high token usage are
//     penalised (scaled to median)
//
// The winner is the candidate with the highest
// score; ties are broken by lower index. The
// returned Score is normalised to [0, 1].
type HeuristicJudge struct{}

// NewHeuristicJudge returns a Judge that uses
// local signals only (no LLM call).
func NewHeuristicJudge() *HeuristicJudge { return &HeuristicJudge{} }

// Judge implements the Judge interface.
func (h *HeuristicJudge) Judge(_ context.Context, _ string, cands []Candidate) (Verdict, error) {
	if len(cands) == 0 {
		return Verdict{}, fmt.Errorf("darwin: heuristic: no candidates")
	}
	scores := make([]float64, len(cands))
	median := tokenMedian(cands)
	for i, c := range cands {
		s := 0.5
		if c.Err != nil {
			s -= 0.4
		}
		lc := strings.ToLower(c.Text)
		// "test"/"spec" use word boundary to avoid
		// matching "text"/"respect". "lint"/"fmt"/
		// "vet" use substring because "linter"
		// implies linting and that's a feature.
		// Real changes beat prose: a non-empty
		// worktree diff is the strongest local
		// signal that the agent did the work.
		if c.Diff != "" {
			s += 0.2
		}
		if hasWord(lc, "test") || hasWord(lc, "spec") {
			s += 0.15
		}
		if strings.Contains(lc, "lint") || strings.Contains(lc, "fmt") || strings.Contains(lc, "vet") {
			s += 0.05
		}
		if len(c.Text) == 0 {
			s -= 0.2
		} else if len(c.Text) < 50 {
			s -= 0.1
		} else if len(c.Text) > 4000 {
			s -= 0.1
		}
		if median > 0 {
			ratio := float64(c.Usage.Total) / float64(median)
			if ratio > 1.5 {
				s -= 0.1
			} else if ratio < 0.5 {
				s += 0.05
			}
		}
		if s < 0 {
			s = 0
		} else if s > 1 {
			s = 1
		}
		scores[i] = s
	}
	best := 0
	bestScore := scores[0]
	for i := 1; i < len(scores); i++ {
		if scores[i] > bestScore {
			bestScore = scores[i]
			best = i
		}
	}
	return Verdict{
		WinnerIndex: best,
		Score:       bestScore,
		Reason:      fmt.Sprintf("heuristic: best local signal (score=%.2f)", bestScore),
	}, nil
}

// hasWord reports whether needle appears in s
// surrounded by non-letter runes (or string
// boundaries). This avoids "test" matching the
// end of "text" or the start of "testify".
func hasWord(s, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(s); {
		// Left boundary: the rune BEFORE i must
		// be a non-letter (or i==0 is a
		// start-of-string boundary).
		leftOK := i == 0 || !isLetter(s[i-1])
		// Right boundary: the rune AT j must be a
		// non-letter (or j==len is an
		// end-of-string boundary).
		j := i + len(needle)
		rightOK := j == len(s) || !isLetter(s[j])
		if leftOK && rightOK && s[i:j] == needle {
			return true
		}
		i++
	}
	return false
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// tokenMedian returns the median Total token
// count across candidates. Zero if no candidate
// has usage.
func tokenMedian(cands []Candidate) int {
	if len(cands) == 0 {
		return 0
	}
	usages := make([]int, 0, len(cands))
	for _, c := range cands {
		t := c.Usage.Total
		if t > 0 {
			usages = append(usages, t)
		}
	}
	if len(usages) == 0 {
		return 0
	}
	// Simple selection sort; len <= maxPoolSize.
	for i := 1; i < len(usages); i++ {
		for j := i; j > 0 && usages[j-1] > usages[j]; j-- {
			usages[j-1], usages[j] = usages[j], usages[j-1]
		}
	}
	return usages[len(usages)/2]
}

// LLMJudge asks an LLM to pick the best candidate.
// The prompt is plain markdown with all candidate
// texts (truncated). The response contract is
// strict JSON: {winner, score, reason}.
//
// Use CompositeJudge for a production setup — this
// raw LLMJudge has no fallback.
type LLMJudge struct {
	Provider llm.Provider
	Model    string // optional override; "" uses provider default
	// MaxTokens is the max_tokens for the judge
	// call. Default 300.
	MaxTokens int
}

// NewLLMJudge returns a Judge that uses the given
// provider. The provider must support the OpenAI
// chat completions format with a `model` field.
func NewLLMJudge(p llm.Provider, model string) *LLMJudge {
	return &LLMJudge{Provider: p, Model: model}
}

// judgeSchema is the JSON contract the LLM is
// expected to return.
type judgeSchema struct {
	Winner int     `json:"winner"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// codeFenceRE matches ```json ... ``` blocks
// (with or without language tag) for tolerant
// parsing.
var codeFenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// Judge implements the Judge interface.
func (j *LLMJudge) Judge(ctx context.Context, prompt string, cands []Candidate) (Verdict, error) {
	if j == nil || j.Provider == nil {
		return Verdict{}, fmt.Errorf("darwin: LLMJudge: no provider")
	}
	if len(cands) == 0 {
		return Verdict{}, fmt.Errorf("darwin: LLMJudge: no candidates")
	}
	max := j.MaxTokens
	if max <= 0 {
		max = 300
	}
	sys := "You are a strict judge evaluating parallel code-writing agents. Pick the best candidate. Some candidates include a git diff of the actual changes they made in their isolated worktree; weigh the diff over the prose — a candidate whose diff really implements the task beats one that only describes a solution. Reply ONLY with JSON: {\"winner\": <1-based index>, \"score\": <0..1>, \"reason\": <one short sentence>}. No prose, no markdown."
	user := renderJudgePrompt(prompt, cands)
	// Call the provider.
	stream, err := j.Provider.Complete(llm.WithPurpose(ctx, llm.PurposeDarwin), []llm.Message{{
		Role:    llm.RoleUser,
		Content: sys + "\n\n" + user,
	}}, nil) // judge doesn't need tools
	if err != nil {
		return Verdict{}, fmt.Errorf("darwin: LLMJudge: provider: %w", err)
	}
	var buf strings.Builder
	for d := range stream {
		if d.Err != nil {
			return Verdict{}, fmt.Errorf("darwin: LLMJudge: stream: %w", d.Err)
		}
		if d.Content != "" {
			buf.WriteString(d.Content)
		}
	}
	verdict, perr := parseJudgeVerdict(buf.String(), len(cands))
	if perr != nil {
		return Verdict{}, perr
	}
	return verdict, nil
}

// renderJudgePrompt builds the user-side prompt
// for the LLM judge. Each candidate's text is
// truncated to keep the total under ~16K chars.
func renderJudgePrompt(prompt string, cands []Candidate) string {
	var b strings.Builder
	b.WriteString("Task: ")
	b.WriteString(prompt)
	b.WriteString("\n\nCandidates:\n")
	for i, c := range cands {
		fmt.Fprintf(&b, "--- candidate %d", i+1)
		if c.Err != nil {
			fmt.Fprintf(&b, " (FAILED: %v)", c.Err)
		}
		b.WriteString(" ---\n")
		t := c.Text
		const maxCand = 4000
		if len(t) > maxCand {
			t = t[:maxCand] + "...[truncated]"
		}
		b.WriteString(t)
		b.WriteString("\n")
		if c.Diff != "" {
			d := c.Diff
			const maxDiff = 8000
			if len(d) > maxDiff {
				d = d[:maxDiff] + "\n...[diff truncated]"
			}
			b.WriteString("Diff of changes made by this candidate:\n```diff\n")
			b.WriteString(d)
			b.WriteString("\n```\n")
		}
	}
	b.WriteString("\nReply with JSON: {\"winner\": <1-based int>, \"score\": <0..1>, \"reason\": <one short sentence>}")
	return b.String()
}

// parseJudgeVerdict extracts winner/score/reason
// from a model response. Tolerant of:
//   - code fences (```json ... ```)
//   - leading/trailing prose
//   - missing score (defaults to 0.5)
//   - winner out of range (returns error)
func parseJudgeVerdict(s string, n int) (Verdict, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Verdict{}, fmt.Errorf("darwin: empty judge response")
	}
	body := s
	if m := codeFenceRE.FindStringSubmatch(s); len(m) >= 2 {
		body = m[1]
	} else {
		// Look for the first '{' and last '}'.
		i := strings.IndexByte(s, '{')
		j := strings.LastIndexByte(s, '}')
		if i >= 0 && j > i {
			body = s[i : j+1]
		}
	}
	var sch judgeSchema
	if err := json.Unmarshal([]byte(body), &sch); err != nil {
		return Verdict{}, fmt.Errorf("darwin: parse judge: %w (body=%q)", err, body)
	}
	if sch.Winner < 1 || sch.Winner > n {
		return Verdict{}, fmt.Errorf("darwin: judge winner %d out of range [1, %d]", sch.Winner, n)
	}
	if sch.Score < 0 {
		sch.Score = 0
	}
	if sch.Score > 1 {
		sch.Score = 1
	}
	return Verdict{
		WinnerIndex: sch.Winner - 1, // 0-based for internal use
		Score:       sch.Score,
		Reason:      strings.TrimSpace(sch.Reason),
	}, nil
}

// CompositeJudge is the production default. It
// tries the LLM judge first; on any error (no
// provider, parse failure, network, etc.) it falls
// back to the heuristic judge. This keeps Darwin
// useful when the user's primary provider is down.
type CompositeJudge struct {
	Primary Judge
	Fallback Judge
}

// NewCompositeJudge returns a Judge that prefers
// primary but degrades to fallback.
func NewCompositeJudge(primary, fallback Judge) *CompositeJudge {
	return &CompositeJudge{Primary: primary, Fallback: fallback}
}

// Judge implements the Judge interface.
func (c *CompositeJudge) Judge(ctx context.Context, prompt string, cands []Candidate) (Verdict, error) {
	if c.Primary != nil {
		v, err := c.Primary.Judge(ctx, prompt, cands)
		if err == nil && v.WinnerIndex >= 0 && v.WinnerIndex < len(cands) {
			return v, nil
		}
	}
	if c.Fallback != nil {
		return c.Fallback.Judge(ctx, prompt, cands)
	}
	return Verdict{}, fmt.Errorf("darwin: composite: primary failed and no fallback set")
}
