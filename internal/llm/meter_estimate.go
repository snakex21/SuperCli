package llm

// Cheap, calibrated prompt-token estimator for compaction decisions.
// This is THE message-slice estimator — the agent loop, the /context
// report, resume and the light-route chat window all use it, so a
// calibration fix lands everywhere at once.
//
// Calibration (live llama-server logs, Qwen3.5-9B-Q8_0, 2026-07-05
// cache hunt, 27 requests with server-reported prompt_n):
//
//	len/4           mean 0.77 of actual (underestimates 23-32%)
//	non-ws/4        mean 0.67 of actual
//	non-ws/3        mean 0.89 of actual
//	non-ws/3 + 16/msg  mean 0.95, range 0.86-1.02   <- chosen
//
// The per-message constant covers chat-template framing (role
// headers, tool-call wrappers). For cl100k-family cloud models this
// slightly overestimates plain English (~10-15%), which is the safe
// direction for a compaction trigger: compacting a little early is
// cheap, overflowing the window is not.
const (
	estBytesPerToken  = 3
	estPerMessageCost = 16
)

// nonWhitespaceLen counts the bytes of s that are not spaces, tabs
// or newlines. Whitespace runs (code indentation, blank lines)
// compress to almost nothing in BPE vocabularies, so counting them
// would overestimate indented code relative to prose.
func nonWhitespaceLen(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
		default:
			n++
		}
	}
	return n
}

// EstimateMessageTokens estimates the prompt-token cost of one
// message, including tool-call names and arguments — the previous
// chars/4 helpers ignored ToolCalls, so a big write_file/edit call
// was invisible to the compaction trigger.
func EstimateMessageTokens(m Message) int {
	b := nonWhitespaceLen(m.Content)
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			b += nonWhitespaceLen(p.Text)
		}
	}
	for _, tc := range m.ToolCalls {
		b += nonWhitespaceLen(tc.Name) + nonWhitespaceLen(tc.Arguments)
	}
	return b/estBytesPerToken + estPerMessageCost
}

// EstimateTokens sums EstimateMessageTokens over msgs.
func EstimateTokens(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += EstimateMessageTokens(m)
	}
	return n
}
