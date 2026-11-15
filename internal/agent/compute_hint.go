package agent

import (
	"regexp"
	"strings"
)

// Compute hint: questions whose answer is an exact aggregate over files or
// data (counts, totals, percentiles, top-N, sorting many rows, cycles in a
// dependency graph) were answered by tallying in the reply, which a model
// gets wrong on more than a few dozen items. The 1.0.28 diagnostic found the
// passing runs of such tasks computed the result with a script and the
// failing ones counted by hand. On a coordinator Run whose request asks for
// such a result, the request tail says to compute it with ctx_execute. No
// model call; nothing is added to other requests.
const computeHintText = "Exact counts, totals, averages, percentiles, top-N lists, sorted tables of many rows and graph questions (cycles, dependencies) over files or data: compute them with a short script via ctx_execute and use its output; do not tally, sort or trace them by hand."

var computeRequest = regexp.MustCompile(`(?i)\b(?:how many|number of|count(?:s|ing)? (?:of|the|all|every|distinct|unique)|distinct|unique (?:users|values|ids|items)|percentile|p(?:50|90|95|99)\b|median|average|mean\b|top[ -]?\d+|top (?:five|ten|three)|sorted by|sort(?:ed)? (?:them |the \w+ )?by|group(?:ed)? by|per (?:month|day|week|region|account|user|endpoint|route|file)|totals?\b|sum of|every cycle|cycles?\b|dependency cycle|ile (?:jest|było|razy)|policz|zlicz|suma|średni\w*|percentyl\w*|posortowan\w*|posortuj|unikaln\w*|cykl\w*)`)

// computeHint returns the hint for prompt when ctx_execute is available.
func (l *Loop) computeHint(prompt string) string {
	if l.registry == nil {
		return ""
	}
	if _, ok := l.registry.Get("ctx_execute"); !ok {
		return ""
	}
	if !computeRequest.MatchString(strings.ToLower(prompt)) {
		return ""
	}
	return computeHintText
}
