package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// contextStillTooLargeError: the next request does not fit the context window
// even after the regular prune and compaction. Before 1.0.28 the Run ended on
// it with an empty answer (seen with a 16k window at higher reasoning effort).
// Now one emergency prune (protecting only the current step's results) gets
// one retry, and if the request still does not fit, the host writes the final
// answer itself: what was changed in this Run and why it had to stop.
type contextStillTooLargeError struct {
	tokens, window int
}

func (e *contextStillTooLargeError) Error() string {
	return fmt.Sprintf("agent: request exceeds context window after compaction (%d tokens, window %d); active user turn is preserved. %s", e.tokens, e.window, contextWindowRemedy)
}

func isContextStillTooLarge(err error) bool {
	var e *contextStillTooLargeError
	return errors.As(err, &e)
}

// emergencyPrune prunes every older tool result it can, keeping only the
// current step's results, and returns the estimated tokens reclaimed.
func (l *Loop) emergencyPrune(ctx context.Context, out chan<- Event) int {
	if l.pruneProtect < 0 {
		return 0 // pruning disabled in config
	}
	saved := l.pruneProtect
	l.pruneProtect = 1
	l.pruneRefusedEst = 0
	defer func() { l.pruneProtect = saved }()
	return l.maybePruneToolResults(ctx, out)
}

// contextOverflowAnswer is the host-written final answer when the request
// cannot fit the window: never an empty reply.
func (l *Loop) contextOverflowAnswer(err error) string {
	var e *contextStillTooLargeError
	errors.As(err, &e)
	l.completion.mu.Lock()
	files := make([]string, 0, len(l.completion.files))
	for f := range l.completion.files {
		files = append(files, f)
	}
	l.completion.mu.Unlock()
	sort.Strings(files)
	var b strings.Builder
	fmt.Fprintf(&b, "I had to stop before finishing: this conversation no longer fits the model's context window (%d tokens, window %d), even after compacting it.", e.tokens, e.window)
	if len(files) > 0 {
		fmt.Fprintf(&b, " Files changed in this request so far: %s. Anything not done yet is still open.", strings.Join(files, ", "))
	} else {
		b.WriteString(" No files were changed in this request.")
	}
	b.WriteString(" " + contextWindowRemedy + "\n")
	return b.String()
}
