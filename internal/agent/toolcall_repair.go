package agent

// Wave 1 tool-call hardening for small models. Local 3-13B models
// frequently emit tool calls with truncated JSON arguments (cut
// off at max_tokens), unbalanced braces, or misspelled tool names
// ("read_fil" instead of "read_file"). Instead of failing the
// whole call with a raw parse error, we:
//
//  1. try to repair the JSON (close open strings/brackets, strip
//     trailing commas) and proceed when the repaired form parses,
//  2. validate the tool name and suggest the closest known one,
//  3. when the call is beyond repair, send the model a short
//     error with an example of the correct format so it can try
//     again — up to maxToolFormatRetries consecutive times.

import (
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// maxToolFormatRetries is how many consecutive malformed calls we
// answer with a "try again" message before telling the model to
// stop retrying.
const maxToolFormatRetries = 2

// badToolCallMarker prefixes every hardening error we send back to
// the model. recentBadCallStreak counts these in the message tail,
// which keeps retry accounting stateless.
const badToolCallMarker = "invalid tool call: "

// HardenToolCall validates and repairs a tool call in place.
// known is the list of registered tool names. attempt is the
// number of consecutive malformed calls already answered (0 on
// the first failure). It returns "" when the call is fine (args
// may have been repaired in place), or a model-facing error
// message when the call must be bounced back.
func HardenToolCall(tc *llm.ToolCall, known []string, attempt int) string {
	// 1. Tool name validation with did-you-mean.
	if !containsName(known, tc.Name) {
		if containsName(known, "goal") {
			if advice := goalActionToolAdvice(tc.Name, attempt); advice != "" {
				return advice
			}
		}
		suggestion := SuggestToolName(tc.Name, known)
		msg := fmt.Sprintf("%sunknown tool %q.", badToolCallMarker, tc.Name)
		if suggestion != "" {
			msg += fmt.Sprintf(" Did you mean %q?", suggestion)
		}
		return msg + retryAdvice(tc.Name, suggestion, attempt)
	}

	// 2. Argument repair. Empty arguments mean "{}".
	args := strings.TrimSpace(tc.Arguments)
	if args == "" {
		tc.Arguments = "{}"
		return ""
	}
	if json.Valid([]byte(args)) {
		tc.Arguments = args
		return ""
	}
	if fixed, ok := RepairToolArguments(args); ok {
		tc.Arguments = fixed
		return ""
	}

	// 3. Beyond repair: bounce it back with the correct format.
	return fmt.Sprintf("%sthe arguments for %q are not valid JSON (possibly truncated).",
		badToolCallMarker, tc.Name) + retryAdvice(tc.Name, tc.Name, attempt)
}

// goalActionToolAdvice catches a common local-model mistake: the values of
// goal.action are emitted as standalone tool names. Keep Goal dormant until it
// is needed, but give the model the exact compact correction instead of the
// generic placeholder call that tends to cause another wasted inference.
func goalActionToolAdvice(name string, attempt int) string {
	var args string
	switch name {
	case "start_task", "complete_task", "skip_task":
		args = fmt.Sprintf(`{"action":%q,"task_seq":1}`, name)
	case "verify":
		args = `{"action":"verify","passed":true,"text":"concrete check and result"}`
	case "mark_done", "abandon", "show", "list", "tasks":
		args = fmt.Sprintf(`{"action":%q}`, name)
	case "add_task":
		args = `{"action":"add_task","title":"next concrete step"}`
	case "add_note":
		args = `{"action":"add_note","text":"progress or blocker"}`
	default:
		return ""
	}
	msg := fmt.Sprintf(`%s%q is an action of the "goal" tool, not a standalone tool. Activate "goal" with tool_search if needed, then call {"name":"goal","arguments":%s}.`,
		badToolCallMarker, name, args)
	if attempt >= maxToolFormatRetries {
		return msg + " Do not repeat the standalone action name."
	}
	return msg
}

// retryAdvice appends a format example and an invitation to retry,
// unless the retry budget is exhausted.
func retryAdvice(badName, goodName string, attempt int) string {
	if attempt >= maxToolFormatRetries {
		return " Do not retry this call again — answer the user in plain text instead, or pick a different tool."
	}
	name := goodName
	if name == "" {
		name = "tool_name"
	}
	return fmt.Sprintf(
		" Call the tool again with valid JSON arguments, e.g. {\"name\": %q, \"arguments\": {\"key\": \"value\"}}. Keep arguments short and close every brace and quote.",
		name)
}

// recentBadCallStreak counts how many trailing tool messages in
// the conversation are hardening errors. It stops at the first
// non-tool-error message, so the streak resets automatically once
// a call succeeds.
func (l *Loop) recentBadCallStreak() int {
	streak := 0
	for i := len(l.Messages) - 1; i >= 0; i-- {
		m := l.Messages[i]
		switch m.Role {
		case llm.RoleTool:
			if strings.HasPrefix(m.Content, badToolCallMarker) {
				streak++
				continue
			}
			return streak
		case llm.RoleAssistant:
			// Assistant turns between bad calls don't break the streak.
			continue
		default:
			return streak
		}
	}
	return streak
}

// containsName reports whether name is in the list (exact match).
func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// SuggestToolName returns the known tool name closest to the
// given (misspelled) name, or "" when nothing is close enough.
// "Close enough" is an edit distance of at most 2, or at most a
// third of the name's length for long names.
func SuggestToolName(name string, known []string) string {
	if name == "" || len(known) == 0 {
		return ""
	}
	lowName := strings.ToLower(name)
	best, bestDist := "", 1<<30
	for _, k := range known {
		d := editDistance(lowName, strings.ToLower(k))
		if d < bestDist {
			best, bestDist = k, d
		}
	}
	limit := 2
	if l := len(name) / 3; l > limit {
		limit = l
	}
	if bestDist <= limit {
		return best
	}
	return ""
}

// editDistance is the classic Levenshtein distance.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = minOf3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func minOf3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// RepairToolArguments attempts to fix truncated or unbalanced
// JSON emitted by a small model. It:
//
//   - trims everything before the first '{',
//   - strips markdown code fences,
//   - closes an unterminated string,
//   - removes a trailing comma or dangling key,
//   - closes open braces/brackets in the right order.
//
// Returns the repaired JSON and true when the result parses.
func RepairToolArguments(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	// Strip markdown fences (```json ... ```).
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	// Trim leading garbage before the first brace.
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	if s == "" || s[0] != '{' {
		return "", false
	}

	// Find the last comma outside of strings so we can drop a
	// dangling truncated tail as a second repair attempt.
	lastComma := -1
	{
		inStr, esc := false, false
		for i := 0; i < len(s); i++ {
			c := s[i]
			if inStr {
				if esc {
					esc = false
					continue
				}
				if c == '\\' {
					esc = true
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			switch c {
			case '"':
				inStr = true
			case ',':
				lastComma = i
			}
		}
	}

	candidate := func(body string) (string, bool) {
		body = strings.TrimSpace(body)
		// Recompute closers for the (possibly shortened) body.
		var st []byte
		inStr, esc := false, false
		for i := 0; i < len(body); i++ {
			c := body[i]
			if inStr {
				if esc {
					esc = false
					continue
				}
				if c == '\\' {
					esc = true
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			switch c {
			case '"':
				inStr = true
			case '{', '[':
				st = append(st, c)
			case '}', ']':
				if len(st) > 0 {
					st = st[:len(st)-1]
				}
			}
		}
		if inStr {
			body += `"`
		}
		body = strings.TrimRight(body, " \t\r\n")
		body = strings.TrimSuffix(body, ",")
		for i := len(st) - 1; i >= 0; i-- {
			if st[i] == '{' {
				body += "}"
			} else {
				body += "]"
			}
		}
		if json.Valid([]byte(body)) {
			return body, true
		}
		return "", false
	}

	// First try: close everything as-is.
	if out, ok := candidate(s); ok {
		return out, true
	}
	// Second try: drop the dangling tail after the last comma
	// (e.g. a truncated `"key": "val` or a lone `"key"`).
	if lastComma >= 0 {
		if out, ok := candidate(s[:lastComma]); ok {
			return out, true
		}
	}
	return "", false
}
