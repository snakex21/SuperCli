package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Retained user-context budget.
//
// Re-embedding every earlier request verbatim made the carry grow
// monotonically: with multi-thousand-token prompts it eventually approached
// the compaction threshold by itself, no summary could reduce the prefix any
// more, and every following turn failed (audit-130 long-session wedge). The
// carry is therefore bounded by a fraction of the context window. Within the
// budget:
//   - the newest carried request stays verbatim when it fits the room the
//     older ones leave (at least half of the request budget); otherwise it is
//     cut to a head+tail excerpt with an explicit omission marker;
//   - each older request keeps at most a head+tail excerpt of 1/8 of the
//     budget (short requests stay verbatim), so constraints at the start or
//     end of a long paste survive;
//   - if that still does not fit, requests after the first one (the original
//     task) are dropped oldest-first and replaced by one counted marker.
//
// Auto compaction never summarizes the active turn or the turn before it, so
// those two requests always remain verbatim outside this carry.
const (
	compactUserContextWindowDivisor = 8     // carry <= 1/8 of the window
	compactUserContextMinBudget     = 512   // tiny windows still keep a usable excerpt
	compactUserContextMaxBudget     = 32768 // huge windows do not carry megabytes of pastes
	compactUserContextOlderDivisor  = 8     // each older request <= 1/8 of the request budget
	compactUserContextPriorDivisor  = 4     // legacy prior state <= 1/4 of the budget
)

// compactUserContextBudget is the token budget for the retained user carry.
// The auto threshold leaves a >= 10% reserve and an accepted summary must save
// 20% of the prefix it replaces; a 1/8-window carry keeps both reachable even
// when every turn is a long paste.
func compactUserContextBudget(window int) int {
	if window <= 0 {
		window = defaultContextWindow
	}
	return min(max(window/compactUserContextWindowDivisor, compactUserContextMinBudget), compactUserContextMaxBudget)
}

type compactWindowKey struct{}

// withCompactWindow tells the summarizer which window bounds the carry.
func withCompactWindow(ctx context.Context, window int) context.Context {
	return context.WithValue(ctx, compactWindowKey{}, window)
}

// WithCompactWindow is withCompactWindow for callers outside the package
// that run a summarizer directly (tests comparing against the loop).
func WithCompactWindow(ctx context.Context, window int) context.Context {
	return withCompactWindow(ctx, window)
}

func compactWindowFrom(ctx context.Context) int {
	w, _ := ctx.Value(compactWindowKey{}).(int)
	return w
}

// compactCarryTokens prices a string as it is embedded in the carry (JSON
// escapes included) at the request estimator's 3 bytes/token, rounded up.
// Whitespace is counted too, so this never underestimates the estimator.
func compactCarryTokens(s string) int {
	return (compactJSONLen(s) + 2) / 3
}

func compactJSONLen(s string) int {
	raw, _ := json.Marshal(s)
	return len(raw)
}

const (
	compactOmittedRequestsPrefix = "[omitted: "
	compactOmittedRequestsSuffix = " earlier user request(s) dropped to fit the compaction budget]"
	compactExcerptMarkerFormat   = "\n…[%d bytes of this user request omitted to fit the compaction budget]…\n"
)

func compactOmittedRequestsMarker(n int) string {
	return compactOmittedRequestsPrefix + strconv.Itoa(n) + compactOmittedRequestsSuffix
}

func compactOmittedRequestsCount(s string) (int, bool) {
	if !strings.HasPrefix(s, compactOmittedRequestsPrefix) || !strings.HasSuffix(s, compactOmittedRequestsSuffix) {
		return 0, false
	}
	n, err := strconv.Atoi(s[len(compactOmittedRequestsPrefix) : len(s)-len(compactOmittedRequestsSuffix)])
	return n, err == nil && n > 0
}

// compactUserExcerpt keeps a head and a tail of s within maxTokens, joined by a
// marker that says how many bytes were cut. Strings that fit are unchanged.
func compactUserExcerpt(s string, maxTokens int) string {
	if compactCarryTokens(s) <= maxTokens {
		return s
	}
	// Raw bytes that encode to roughly maxTokens, given this string's escapes.
	room := maxTokens * 3 * len(s) / max(1, compactJSONLen(s))
	for i := 0; i < 64 && room > 0; i++ {
		head, tail := room/2, len(s)-(room-room/2)
		for head > 0 && !utf8.RuneStart(s[head]) {
			head--
		}
		for tail < len(s) && !utf8.RuneStart(s[tail]) {
			tail++
		}
		if tail < head {
			tail = head
		}
		out := s[:head] + fmt.Sprintf(compactExcerptMarkerFormat, tail-head) + s[tail:]
		over := compactCarryTokens(out) - maxTokens
		if over <= 0 {
			return out
		}
		room -= max(room/16, 3*over, 1)
	}
	return strings.TrimSpace(fmt.Sprintf(compactExcerptMarkerFormat, len(s)))
}

// boundCompactUserContext applies the carry policy above. It is idempotent for
// a fixed budget, so importing an already bounded carry on the next compaction
// does not cut it again.
func boundCompactUserContext(carry compactUserContext, budget int) compactUserContext {
	var out compactUserContext
	used := 0
	if len(carry.PriorState) > 0 {
		each := max(1, budget/compactUserContextPriorDivisor/len(carry.PriorState))
		for _, state := range carry.PriorState {
			state = compactUserExcerpt(state, each)
			out.PriorState = append(out.PriorState, state)
			used += compactCarryTokens(state)
		}
	}
	n := len(carry.Requests)
	if n == 0 {
		return out
	}
	reqBudget := max(budget-used, budget/2)
	older := max(1, reqBudget/compactUserContextOlderDivisor)
	reqs := make([]string, n)
	olderUsed := 0
	for i, r := range carry.Requests[:n-1] {
		if _, marker := compactOmittedRequestsCount(r); !marker {
			r = compactUserExcerpt(r, older)
		}
		reqs[i] = r
		olderUsed += compactCarryTokens(r)
	}
	reqs[n-1] = compactUserExcerpt(carry.Requests[n-1], max(reqBudget-olderUsed, reqBudget/2))

	total := olderUsed + compactCarryTokens(reqs[n-1])
	// Drop the oldest requests after the first one (the original task) until
	// the carry fits; earlier omission markers fold into a single count.
	omitted := 0
	markerCost := func() int {
		if omitted == 0 {
			return 0
		}
		return compactCarryTokens(compactOmittedRequestsMarker(omitted))
	}
	for len(reqs) > 2 && total+markerCost() > reqBudget {
		victim := reqs[1]
		if c, ok := compactOmittedRequestsCount(victim); ok {
			omitted += c
		} else {
			omitted++
		}
		total -= compactCarryTokens(victim)
		reqs = append(reqs[:1], reqs[2:]...)
	}
	if omitted > 0 {
		marker := compactOmittedRequestsMarker(omitted)
		reqs = append(reqs[:1], append([]string{marker}, reqs[1:]...)...)
	}
	out.Requests = reqs
	return out
}
