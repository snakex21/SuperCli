package core

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ModelContentTailBytes caps how much of Result.Text is inlined
// into the failure message the model sees. Same 2 KB tail-keeping
// convention as the c74e100 read caps and the ctxexec/usertools
// FailTailBytes: the LAST bytes are where errors usually are, so
// a retry can self-correct without a second round-trip.
const ModelContentTailBytes = 2048

// ModelContent renders a Result as the single string the agent
// loop puts into the tool-result message. It is the ONLY place
// that decides what the model sees:
//
//   - success (Err == nil): Text verbatim — happy path untouched.
//   - failure with no Text: "error: <err>".
//   - failure with Text: "error: <err>" plus a capped tail of
//     Text, so diagnostics a tool put next to its error are not
//     silently dropped (the historical ctx_execute bug: the loop
//     used only Err and the stderr the model needed vanished).
//
// Deduplication: when the error was marked with SelfContainedErr,
// or the error message already contains the (trimmed) Text, the
// Text is NOT appended a second time.
func (r Result) ModelContent() string {
	if r.Err == nil {
		return r.Text
	}
	msg := "error: " + r.Err.Error()
	if isSelfContained(r.Err) {
		return msg
	}
	text := strings.TrimSpace(r.Text)
	if text == "" || strings.Contains(msg, text) {
		return msg
	}
	label := "tool output"
	if len(text) > ModelContentTailBytes {
		// Keep the LAST bytes, cut on a rune boundary (at most
		// 3 bytes forward) so a multi-byte character is never
		// split in half.
		i := len(text) - ModelContentTailBytes
		for i < len(text) && !utf8.RuneStart(text[i]) {
			i++
		}
		text = text[i:]
		label = "tool output (tail, truncated)"
	}
	return msg + "\n" + label + ":\n" + text
}

// selfContainedError marks an error whose message already carries
// every diagnostic the model needs (e.g. a structured
// "command_failed exit=N" failure that embeds the output tail).
// ModelContent will not append Result.Text for such errors, so a
// tool can keep a rich Text for UIs without the model paying for
// the same bytes twice.
type selfContainedError struct{ err error }

func (e selfContainedError) Error() string { return e.err.Error() }
func (e selfContainedError) Unwrap() error { return e.err }

// SelfContainedErr wraps err so ModelContent treats its message
// as complete and skips the Result.Text suffix. Wrapping nil
// returns nil.
func SelfContainedErr(err error) error {
	if err == nil {
		return nil
	}
	return selfContainedError{err: err}
}

// isSelfContained reports whether err (or anything it wraps) was
// marked with SelfContainedErr.
func isSelfContained(err error) bool {
	var sc selfContainedError
	return errors.As(err, &sc)
}
