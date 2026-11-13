package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"supercli/internal/agenteval"
)

// ledger accumulates the cost of runs. An existing ledger file is
// continued, so the budget spans several invocations.
type ledger struct {
	o     options
	total float64
	rows  int
}

func newLedger(o options) *ledger {
	l := &ledger{o: o}
	if o.ledger == "" {
		return l
	}
	data, err := os.ReadFile(o.ledger)
	if err != nil {
		header := "# OpenAI ledger (agent eval suite)\n\n" +
			fmt.Sprintf("Pricing per 1M tokens: input $%.3f, cached $%.3f, output $%.3f.\n\n", o.priceIn, o.priceCached, o.priceOut) +
			"| # | time | label | task | rep | model | in | cached | out | reasoning | cost $ | cumulative $ |\n" +
			"|---|------|-------|------|-----|-------|----|--------|-----|-----------|--------|--------------|\n"
		_ = os.WriteFile(o.ledger, []byte(header), 0o644)
		return l
	}
	for _, line := range strings.Split(string(data), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 13 {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(cells[1])); err != nil {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(cells[12]), 64); err == nil {
			l.total = v
			l.rows++
		}
	}
	return l
}

func (l *ledger) add(run agenteval.RunReport) float64 {
	m := run.Metrics
	if m == nil {
		return 0
	}
	cost := (float64(m.Input-m.Cached)*l.o.priceIn + float64(m.Cached)*l.o.priceCached + float64(m.Output)*l.o.priceOut) / 1e6
	l.total += cost
	l.rows++
	if l.o.ledger != "" {
		row := fmt.Sprintf("| %d | %s | %s | %s | %d | %s | %d | %d | %d | %d | %.4f | %.4f |\n", l.rows, time.Now().Format("15:04:05"),
			l.o.label, run.TaskID, run.Repeat, m.Model, m.Input, m.Cached, m.Output, m.Reasoning, cost, l.total)
		if f, err := os.OpenFile(l.o.ledger, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(row)
			_ = f.Close()
		}
	}
	return cost
}

func renderMarkdown(r fullReport, suite agenteval.Suite) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# SuperCli agent suite: %s\n\n", r.Label)
	fmt.Fprintf(&b, "- suite: `%s`, provider `%s`, model `%s`, agent `%s`, %s\n", r.SuiteID, r.Provider, r.Model, r.Agent, r.Platform)
	fmt.Fprintf(&b, "- started %s, wall %s, repeats %d, cost $%.4f\n", r.StartedAt, (time.Duration(r.WallMs) * time.Millisecond).Round(time.Second), r.Repeats, r.CostUSD)
	graded := len(r.Runs) - r.Skipped
	fmt.Fprintf(&b, "- runs %d: pass %d, partial %d, fail %d, skipped %d (pass rate %.0f%%, mean score %.2f)\n\n",
		len(r.Runs), r.Passed, r.Partial, r.Failed-r.Partial, r.Skipped, pct(r.Passed, graded), meanScore(r.Runs))

	categories := map[string]string{}
	order := []string{}
	for _, t := range suite.Tasks {
		categories[t.ID] = t.Category
		order = append(order, t.ID)
	}
	byTask := map[string][]agenteval.RunReport{}
	for _, run := range r.Runs {
		byTask[run.TaskID] = append(byTask[run.TaskID], run)
	}

	b.WriteString("## Per task\n\n| task | category | status per repeat | score mean (sd) | tool calls | input / cached / output / reasoning tokens | wall s | failure |\n|---|---|---|---|---|---|---|---|\n")
	for _, id := range order {
		runs := byTask[id]
		if len(runs) == 0 {
			continue
		}
		var statuses, failures []string
		var scores []float64
		var tools, in, cached, out, reasoning, wall []float64
		for _, run := range runs {
			statuses = append(statuses, run.Status)
			if run.Status == agenteval.StatusSkipped {
				continue
			}
			scores = append(scores, run.Score)
			if run.FailureCategory != "" {
				failures = append(failures, run.FailureCategory)
			}
			wall = append(wall, float64(run.DurationMs)/1000)
			if m := run.Metrics; m != nil {
				tools = append(tools, float64(m.ToolCalls))
				in = append(in, float64(m.Input))
				cached = append(cached, float64(m.Cached))
				out = append(out, float64(m.Output))
				reasoning = append(reasoning, float64(m.Reasoning))
			}
		}
		mean, sd := meanSD(scores)
		fmt.Fprintf(&b, "| %s | %s | %s | %.2f (%.2f) | %s | %s / %s / %s / %s | %s | %s |\n", id, categories[id], strings.Join(statuses, ", "), mean, sd,
			joinNums(tools), joinNums(in), joinNums(cached), joinNums(out), joinNums(reasoning), joinNums(wall), strings.Join(uniq(failures), ", "))
	}

	b.WriteString("\n## Per category\n\n| category | runs | pass | partial | fail | mean score |\n|---|---|---|---|---|---|\n")
	type agg struct {
		runs, pass, partial, fail int
		score                     float64
	}
	cats := map[string]*agg{}
	var catOrder []string
	for _, id := range order {
		for _, run := range byTask[id] {
			if run.Status == agenteval.StatusSkipped {
				continue
			}
			c := categories[id]
			a := cats[c]
			if a == nil {
				a = &agg{}
				cats[c] = a
				catOrder = append(catOrder, c)
			}
			a.runs++
			a.score += run.Score
			switch run.Status {
			case agenteval.StatusPass:
				a.pass++
			case agenteval.StatusPartial:
				a.partial++
			default:
				a.fail++
			}
		}
	}
	for _, c := range catOrder {
		a := cats[c]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %.2f |\n", c, a.runs, a.pass, a.partial, a.fail, a.score/float64(a.runs))
	}

	failCount := map[string]int{}
	for _, run := range r.Runs {
		if run.FailureCategory != "" {
			failCount[run.FailureCategory]++
		}
	}
	if len(failCount) > 0 {
		b.WriteString("\n## Failure categories\n\n| category | runs |\n|---|---|\n")
		keys := make([]string, 0, len(failCount))
		for k := range failCount {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return failCount[keys[i]] > failCount[keys[j]] })
		for _, k := range keys {
			fmt.Fprintf(&b, "| %s | %d |\n", k, failCount[k])
		}
	}

	b.WriteString("\n## Non-passing runs\n\n")
	for _, run := range r.Runs {
		if run.Passed || run.Status == agenteval.StatusSkipped {
			continue
		}
		fmt.Fprintf(&b, "### %s r%d: %s (score %.2f, %s)\n\n", run.TaskID, run.Repeat, run.Status, run.Score, run.FailureCategory)
		if run.AgentError != "" {
			fmt.Fprintf(&b, "- agent error: %s\n", oneLine(run.AgentError, 300))
		}
		if len(run.ForbiddenChanges)+len(run.DisallowedChanges) > 0 {
			fmt.Fprintf(&b, "- forbidden/disallowed changes: %v %v\n", run.ForbiddenChanges, run.DisallowedChanges)
		}
		if len(run.OutsideWrites) > 0 {
			fmt.Fprintf(&b, "- writes outside the workspace: %v\n", run.OutsideWrites)
		}
		if len(run.MissingChanges) > 0 {
			fmt.Fprintf(&b, "- expected but unchanged: %v\n", run.MissingChanges)
		}
		for _, c := range run.Checks {
			if c.Passed {
				continue
			}
			detail := c.Detail
			if detail == "" {
				detail = lastLines(c.OutputTail, 4)
			}
			fmt.Fprintf(&b, "- check `%s` (%s%s) failed: %s\n", c.ID, c.Kind, critical(c.Critical), oneLine(detail, 400))
		}
		if run.ClaimsSuccess {
			b.WriteString("- the final answer claims success although checks failed\n")
		}
		if t, ok := run.Extra["transcript"].(string); ok {
			fmt.Fprintf(&b, "- transcript: `%s`\n", t)
		}
		fmt.Fprintf(&b, "- answer: %s\n\n", oneLine(run.Answer, 400))
	}
	return b.String()
}

func critical(c bool) string {
	if c {
		return ", critical"
	}
	return ""
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func meanScore(runs []agenteval.RunReport) float64 {
	var s []float64
	for _, r := range runs {
		if r.Status != agenteval.StatusSkipped {
			s = append(s, r.Score)
		}
	}
	m, _ := meanSD(s)
	return m
}

func meanSD(v []float64) (float64, float64) {
	if len(v) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	mean := sum / float64(len(v))
	var ss float64
	for _, x := range v {
		ss += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(ss / float64(len(v)))
}

func joinNums(v []float64) string {
	if len(v) == 0 {
		return "-"
	}
	parts := make([]string, len(v))
	for i, x := range v {
		if x >= 10000 {
			parts[i] = fmt.Sprintf("%.0fk", x/1000)
		} else if x == math.Trunc(x) {
			parts[i] = fmt.Sprintf("%.0f", x)
		} else {
			parts[i] = fmt.Sprintf("%.1f", x)
		}
	}
	return strings.Join(parts, "/")
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
