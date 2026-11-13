package llm

import (
	"fmt"
	"strings"
)

// runawayToolArgsWindow is how much trailing tool-call argument text must be
// nothing but whitespace and JSON whitespace escapes before the stream is
// stopped. Live (gpt-5.4-nano on the Responses API) a ctx_execute call
// degenerated into " \n \n ..." and streamed ~70k output tokens over seven
// minutes before the benchmark harness killed it; nothing in SuperCli capped
// it. 8 KiB (~3k tokens) is far beyond any legitimate blank run in arguments.
const runawayToolArgsWindow = 8 << 10

// checkRunawayToolArguments inspects args after a delta grew it from prevLen.
// The scan runs only when the length crosses a 1 KiB boundary, so the hot path
// stays a length comparison.
func checkRunawayToolArguments(name string, args *strings.Builder, prevLen int) error {
	n := args.Len()
	if n < runawayToolArgsWindow || n>>10 == prevLen>>10 {
		return nil
	}
	tail := args.String()[n-runawayToolArgsWindow:]
	for i := 0; i < len(tail); i++ {
		switch tail[i] {
		case ' ', '\t', '\r', '\n':
		case '\\':
			if i+1 < len(tail) {
				switch tail[i+1] {
				case 'n', 'r', 't', '\\':
					i++
				}
			}
		case 'n', 'r', 't':
			// The second byte of an escape split across the window edge.
			if i != 0 {
				return nil
			}
		default:
			return nil
		}
	}
	return fmt.Errorf("stopped a runaway tool call: the model streamed %d KB of only whitespace into %q arguments; the request was cancelled to stop paying for more output", n>>10, name)
}
