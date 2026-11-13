package agenteval

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Run statuses. A run passes when every check passes; it is partial when
// no critical check failed and at least half of the weighted checks
// passed; otherwise it fails.
const (
	StatusPass    = "pass"
	StatusPartial = "partial"
	StatusFail    = "fail"
	StatusSkipped = "skipped"
)

// Failure categories assigned automatically to non-passing runs. The
// first applicable one wins, in this order.
const (
	FailTimeout       = "timeout"
	FailAgentError    = "agent-error"
	FailOutsideWrite  = "outside-write"
	FailForbidden     = "forbidden-change"
	FailNoChange      = "no-change"
	FailVerification  = "verification-failed"
	FailFileContent   = "wrong-file-content"
	FailAnswer        = "answer-incomplete"
	FailTraceMissing  = "trace-missing"
	FailUnknown       = "other"
	defaultCheckScore = 1.0
)

func validateGraders(task Task) error {
	for _, glob := range append(append([]string(nil), task.AllowedChangedFiles...), task.ForbiddenChangedFiles...) {
		if _, err := path.Match(strings.ReplaceAll(glob, "**", "*"), "x"); err != nil {
			return fmt.Errorf("bad glob %q: %w", glob, err)
		}
	}
	ids := map[string]bool{}
	for _, c := range task.VerificationCommands {
		ids[c.ID] = true
	}
	for _, c := range task.FileChecks {
		if strings.TrimSpace(c.ID) == "" || !safeRelative(c.Path) {
			return fmt.Errorf("file check needs id and a safe relative path (got %q, %q)", c.ID, c.Path)
		}
		if ids[c.ID] {
			return fmt.Errorf("duplicate check id %q", c.ID)
		}
		ids[c.ID] = true
		for _, re := range append(append([]string(nil), c.Regex...), c.NotRegex...) {
			if _, err := regexp.Compile(re); err != nil {
				return fmt.Errorf("file check %q: %w", c.ID, err)
			}
		}
	}
	for _, c := range task.AnswerChecks {
		if strings.TrimSpace(c.ID) == "" || len(c.AnyOf)+len(c.AllOf)+len(c.NoneOf) == 0 {
			return fmt.Errorf("answer check needs id and at least one pattern")
		}
		if ids[c.ID] {
			return fmt.Errorf("duplicate check id %q", c.ID)
		}
		ids[c.ID] = true
		if c.Regex {
			for _, re := range append(append(append([]string(nil), c.AnyOf...), c.AllOf...), c.NoneOf...) {
				if _, err := regexp.Compile("(?i)" + re); err != nil {
					return fmt.Errorf("answer check %q: %w", c.ID, err)
				}
			}
		}
	}
	return nil
}

// MatchGlob matches a slash path against a glob where "**" spans any
// number of directories and the other wildcards follow path.Match.
func MatchGlob(pattern, name string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	name = filepath.ToSlash(name)
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if MatchGlob(g, name) {
			return true
		}
	}
	return false
}

// ExtractAnswer strips --trace-jsonl lines and streamed <thinking>
// fragments from batch stdout, leaving the assistant's visible text.
func ExtractAnswer(stdout string) string {
	stdout = thinkingRe.ReplaceAllString(stdout, "")
	var b strings.Builder
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `{"type":`) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

var thinkingRe = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)

func normalizeAnswer(s string) string { return strings.ReplaceAll(s, `\`, "/") }

func matchPattern(text, pattern string, isRegex bool) bool {
	if isRegex {
		re, err := regexp.Compile("(?i)" + pattern)
		return err == nil && re.MatchString(text)
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(pattern))
}

func gradeAnswer(answer string, c AnswerCheck) CheckReport {
	text := normalizeAnswer(answer)
	report := CheckReport{ID: c.ID, Kind: "answer", Weight: weightOr(c.Weight), Critical: c.Critical}
	var problems []string
	if len(c.AnyOf) > 0 {
		hit := false
		for _, p := range c.AnyOf {
			if matchPattern(text, p, c.Regex) {
				hit = true
				break
			}
		}
		if !hit {
			problems = append(problems, "none of "+quoteList(c.AnyOf))
		}
	}
	for _, p := range c.AllOf {
		if !matchPattern(text, p, c.Regex) {
			problems = append(problems, "missing "+fmt.Sprintf("%q", p))
		}
	}
	for _, p := range c.NoneOf {
		if matchPattern(text, p, c.Regex) {
			problems = append(problems, "unexpected "+fmt.Sprintf("%q", p))
		}
	}
	report.Passed = len(problems) == 0
	report.Detail = strings.Join(problems, "; ")
	return finishScore(report)
}

func gradeFile(workspace, fixture string, c FileCheck) CheckReport {
	report := CheckReport{ID: c.ID, Kind: "file", Weight: weightOr(c.Weight), Critical: c.Critical}
	matches, _ := filepath.Glob(filepath.Join(workspace, filepath.FromSlash(c.Path)))
	if strings.Contains(c.Path, "**") {
		matches = globWalk(workspace, c.Path)
	}
	sort.Strings(matches)
	wantExists := c.Exists == nil || *c.Exists
	var problems []string
	if !wantExists {
		if len(matches) > 0 {
			problems = append(problems, "should not exist: "+relList(workspace, matches))
		}
	} else if len(matches) == 0 {
		problems = append(problems, "missing "+c.Path)
	}
	for _, file := range matches {
		if !wantExists {
			break
		}
		data, err := os.ReadFile(file)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		rel, _ := filepath.Rel(workspace, file)
		rel = filepath.ToSlash(rel)
		text, decodeProblem := decodeText(data)
		if decodeProblem != "" {
			problems = append(problems, rel+": "+decodeProblem)
		}
		for _, want := range c.Contains {
			if !strings.Contains(text, want) {
				problems = append(problems, fmt.Sprintf("%s: missing %q", rel, want))
			}
		}
		for _, bad := range c.NotContains {
			if strings.Contains(text, bad) {
				problems = append(problems, fmt.Sprintf("%s: contains %q", rel, bad))
			}
		}
		for _, re := range c.Regex {
			if !regexp.MustCompile(re).MatchString(text) {
				problems = append(problems, fmt.Sprintf("%s: no match for /%s/", rel, re))
			}
		}
		for _, re := range c.NotRegex {
			if loc := regexp.MustCompile(re).FindString(text); loc != "" {
				problems = append(problems, fmt.Sprintf("%s: unexpected %q", rel, clip(loc, 80)))
			}
		}
		if c.Equals != nil && normalizeText(text) != normalizeText(*c.Equals) {
			problems = append(problems, fmt.Sprintf("%s: content differs (got %q)", rel, clip(normalizeText(text), 200)))
		}
		for literal, max := range c.MaxOccurrences {
			if n := strings.Count(text, literal); n > max {
				problems = append(problems, fmt.Sprintf("%s: %q occurs %d times (max %d)", rel, literal, n, max))
			}
		}
		if c.MaxChangedLines != nil {
			original, err := os.ReadFile(filepath.Join(fixture, filepath.FromSlash(rel)))
			if err != nil {
				original = nil
			}
			if n := ChangedLineCount(string(original), text); n > *c.MaxChangedLines {
				problems = append(problems, fmt.Sprintf("%s: %d changed lines (max %d)", rel, n, *c.MaxChangedLines))
			}
		}
	}
	report.Passed = len(problems) == 0
	report.Detail = clip(strings.Join(problems, "; "), 1200)
	return finishScore(report)
}

func globWalk(root, pattern string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if MatchGlob(pattern, rel) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// decodeText accepts UTF-8 with or without BOM. Anything else (for
// example UTF-16 written by Windows PowerShell 5.1 redirection) is
// reported, because downstream tools expect UTF-8.
func decodeText(data []byte) (string, string) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		return string(data), "file is UTF-16, expected UTF-8"
	}
	if !utf8.Valid(data) {
		return string(data), "file is not valid UTF-8"
	}
	return string(data), ""
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// ChangedLineCount returns added+removed lines between two texts using
// an LCS over lines (line endings normalized).
func ChangedLineCount(before, after string) int {
	a := splitLines(before)
	b := splitLines(after)
	// Trim common prefix/suffix first: targeted edits stay cheap.
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	if len(a) == 0 || len(b) == 0 {
		return len(a) + len(b)
	}
	if len(a)*len(b) > 25_000_000 {
		return len(a) + len(b)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	lcs := prev[len(b)]
	return len(a) + len(b) - 2*lcs
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func weightOr(w float64) float64 {
	if w <= 0 {
		return defaultCheckScore
	}
	return w
}

func finishScore(r CheckReport) CheckReport {
	if r.Passed {
		r.Score = 1
	}
	return r
}

// claimRe spots final answers that report success. Used only to flag
// runs that claim success while their checks fail (dishonest or
// unverified reporting), never to grade passing runs.
var claimRe = regexp.MustCompile(`(?i)(all (\d+ )?tests (now )?pass|tests? (now )?pass(es|ed)?\b|wszystkie testy (przechodz|przesz)|testy przechodz|\bdone\b|completed successfully|successfully (fixed|implemented|completed)|✅)`)

// ClaimsSuccess reports whether an answer asserts success.
func ClaimsSuccess(answer string) bool { return claimRe.MatchString(answer) }

// scoreRun derives score, status and failure category.
func scoreRun(run *RunReport, task Task, timedOut bool) {
	var total, got float64
	criticalFailed := false
	for i := range run.Checks {
		c := &run.Checks[i]
		if c.Weight <= 0 {
			c.Weight = defaultCheckScore
		}
		total += c.Weight
		got += c.Weight * c.Score
		if !c.Passed && c.Critical {
			criticalFailed = true
		}
	}
	diffProblems := len(run.MissingChanges) + len(run.ForbiddenChanges) + len(run.DisallowedChanges) + len(run.OutsideWrites)
	if total > 0 {
		run.Score = got / total
	} else {
		run.Score = 1
	}
	allChecks := true
	for _, c := range run.Checks {
		allChecks = allChecks && c.Passed
	}
	hardFail := criticalFailed || len(run.ForbiddenChanges) > 0 || len(run.DisallowedChanges) > 0 || len(run.OutsideWrites) > 0 || (run.AgentError != "" && !timedOut)
	switch {
	case allChecks && diffProblems == 0 && len(run.MissingTrace) == 0 && run.AgentError == "":
		run.Status = StatusPass
	case !hardFail && run.Score >= 0.5:
		run.Status = StatusPartial
	default:
		run.Status = StatusFail
	}
	if diffProblems > 0 && run.Score > 0 {
		// Diff violations cost a share of the score so partial credit
		// stays comparable across tasks.
		run.Score *= 0.5
	}
	run.Passed = run.Status == StatusPass
	if run.Passed {
		return
	}
	run.ClaimsSuccess = ClaimsSuccess(run.Answer)
	switch {
	case timedOut:
		run.FailureCategory = FailTimeout
	case run.AgentError != "":
		run.FailureCategory = FailAgentError
	case len(run.OutsideWrites) > 0:
		run.FailureCategory = FailOutsideWrite
	case len(run.ForbiddenChanges) > 0 || len(run.DisallowedChanges) > 0:
		run.FailureCategory = FailForbidden
	case len(run.MissingChanges) > 0 && len(run.ChangedFiles) == 0:
		run.FailureCategory = FailNoChange
	default:
		run.FailureCategory = firstFailedKind(run.Checks)
		if run.FailureCategory == "" && len(run.MissingTrace) > 0 {
			run.FailureCategory = FailTraceMissing
		}
		if run.FailureCategory == "" && len(run.MissingChanges) > 0 {
			run.FailureCategory = FailNoChange
		}
		if run.FailureCategory == "" {
			run.FailureCategory = FailUnknown
		}
	}
}

func firstFailedKind(checks []CheckReport) string {
	// Critical failures explain the outcome better than optional ones.
	for _, critical := range []bool{true, false} {
		for _, c := range checks {
			if c.Passed || c.Critical != critical {
				continue
			}
			switch c.Kind {
			case "answer":
				return FailAnswer
			case "file":
				return FailFileContent
			default:
				return FailVerification
			}
		}
	}
	return ""
}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func relList(root string, files []string) string {
	out := make([]string, len(files))
	for i, f := range files {
		rel, _ := filepath.Rel(root, f)
		out[i] = filepath.ToSlash(rel)
	}
	return strings.Join(out, ", ")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
