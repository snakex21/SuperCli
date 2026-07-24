package core

import "strings"

// unknownArgumentError reports an argument name the tool's schema does not
// declare. It carries the tool's real argument names so Registry.Execute can
// hand the model one self-contained repair instruction.
//
// The list is the point of this type. SuperCli adds nothing to the system
// prompt for this: the correct names cost tokens only in the turn where the
// model actually got them wrong. Session forensics showed that just over half
// of all moves after a tool error were blind repeats of the same call, which is
// what a message that only says "not allowed" invites.
type unknownArgumentError struct {
	path  string   // JSON path of the offending key, e.g. $.file
	valid []string // argument names this schema declares
	hint  string   // nearest declared name when the key is an obvious typo
}

// Error renders the message without a tool name, for callers that validate
// outside Registry.Execute.
func (e *unknownArgumentError) Error() string { return e.messageFor("") }

// messageFor renders the message naming the tool, e.g.
// `$.file: unknown argument; valid arguments for search_code: max, path, query`.
func (e *unknownArgumentError) messageFor(tool string) string {
	var out strings.Builder
	out.WriteString(e.path)
	out.WriteString(": unknown argument")
	if e.hint != "" {
		out.WriteString(` (did you mean "`)
		out.WriteString(e.hint)
		out.WriteString(`"?)`)
	}
	if len(e.valid) == 0 {
		return out.String()
	}
	out.WriteString("; valid arguments")
	if tool != "" {
		out.WriteString(" for ")
		out.WriteString(tool)
	}
	out.WriteString(": ")
	out.WriteString(strings.Join(e.valid, ", "))
	return out.String()
}

// nearestArgument returns the declared name a mistyped key most plausibly meant.
// It fires only on a clear typographic neighbour (one edit for short names, two
// for longer ones) and stays silent when two candidates tie, so the hint is
// never a guess. A semantic mix-up such as "file" for "path" is not a typo and
// deliberately produces no hint - the valid list already covers that case.
func nearestArgument(name string, valid []string) string {
	name = strings.ToLower(name)
	if name == "" || len(valid) == 0 {
		return ""
	}
	limit := 1
	if len(name) >= 5 {
		limit = 2
	}
	best, bestDistance, ties := "", limit+1, 0
	for _, candidate := range valid {
		distance := editDistance(name, strings.ToLower(candidate), limit)
		switch {
		case distance < bestDistance:
			best, bestDistance, ties = candidate, distance, 1
		case distance == bestDistance:
			ties++
		}
	}
	if bestDistance > limit || ties > 1 {
		return ""
	}
	return best
}

// editDistance is Levenshtein distance, giving up as soon as it exceeds limit.
// Argument names are short, so the plain two-row form is more than fast enough.
func editDistance(a, b string, limit int) int {
	over := limit + 1
	if a == b {
		return 0
	}
	if len(a)-len(b) > limit || len(b)-len(a) > limit {
		return over
	}
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		rowBest := current[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
			rowBest = min(rowBest, current[j])
		}
		if rowBest > limit {
			return over
		}
		previous, current = current, previous
	}
	if previous[len(b)] > limit {
		return over
	}
	return previous[len(b)]
}
