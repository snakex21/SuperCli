package consult

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// judgeSchema is the JSON contract the judge LLM
// is expected to return. We accept a 1-based
// winner index and a one-sentence reason. Score
// is intentionally absent (we don't need it for
// a single-winner pick).
type judgeSchema struct {
	Winner int    `json:"winner"`
	Reason string `json:"reason"`
}

// codeFenceRE matches ```json ... ``` blocks
// (with or without language tag) for tolerant
// parsing. Same pattern as darwin/judge.go.
var codeFenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// renderJudgePrompt builds the user-side
// prompt for the judge. Each candidate is
// truncated to `cap` chars so the total stays
// under ~16K.
func renderJudgePrompt(q string, cands []Candidate, cap int) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(q)
	b.WriteString("\n\nCandidates:\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "--- candidate %d (provider: %s) ---\n", c.Index, c.Provider)
		t := c.Response
		if len(t) > cap {
			t = t[:cap] + "...[truncated]"
		}
		b.WriteString(t)
		b.WriteString("\n")
	}
	b.WriteString("\nReply with JSON: {\"winner\": <1-based int>, \"reason\": <one short sentence>}")
	return b.String()
}

// parseJudgeVerdict extracts winner + reason
// from a model response. Tolerant of:
//   - code fences (```json ... ```)
//   - leading/trailing prose
//   - winner out of range (returns error so
//     the tool layer can fall back)
func parseJudgeVerdict(s string, n int) (Verdict, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Verdict{}, fmt.Errorf("empty judge response")
	}
	body := s
	if m := codeFenceRE.FindStringSubmatch(s); len(m) >= 2 {
		body = m[1]
	} else {
		// Find the first '{' and last '}'.
		i := strings.IndexByte(s, '{')
		j := strings.LastIndexByte(s, '}')
		if i >= 0 && j > i {
			body = s[i : j+1]
		}
	}
	var sch judgeSchema
	if err := json.Unmarshal([]byte(body), &sch); err != nil {
		return Verdict{}, fmt.Errorf("parse judge: %w (body=%q)", err, body)
	}
	if sch.Winner < 1 || sch.Winner > n {
		return Verdict{}, fmt.Errorf("judge winner %d out of range [1, %d]", sch.Winner, n)
	}
	return Verdict{
		WinnerIndex: sch.Winner - 1, // 0-based for internal use
		Reason:      strings.TrimSpace(sch.Reason),
	}, nil
}
