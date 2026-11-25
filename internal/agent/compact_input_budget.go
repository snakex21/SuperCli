package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"supercli/internal/llm"
)

// Compaction input within the window (after 1.0.40). A long session (5,121
// messages, never compacted while it ran on a model whose requests stayed
// small) was resumed on a 1M-token model. The summary request then carried
// the whole history at once - every tool result excerpt and every worker
// report, ~3.8 MB of text - and the provider rejected it: "prompt is too
// long: 1295213 tokens > 1000000 maximum". Compaction failed, the turn
// stopped, and every later message failed the same way.
//
// The summary input is now built for the summarizing model's window:
//
//  1. the normal transcript when it fits half the window;
//  2. otherwise the same transcript with tighter caps: older texts and tool
//     results/arguments become short excerpts with an omission marker;
//  3. otherwise a rolling summary over consecutive chunks, each within the
//     budget, every chunk carrying the summary of the previous ones.
//
// When the provider still answers "prompt is too long: X > Y", the real size
// and limit are parsed, the ratio of real to estimated tokens is remembered
// for that model, and the request is rebuilt smaller and retried.

// compactInputShare is the share of the window one summary request may use;
// the rest covers estimate error and the summary itself.
const compactInputShare = 0.5

// compactUnknownWindow is assumed when no window is known.
const compactUnknownWindow = 200_000

// elidedCompactCaps is the second step: short excerpts of everything older
// than the two newest user turns, short tool excerpts everywhere.
var elidedCompactCaps = compactCaps{older: 600, tool: 160}

// maxCompactRetries bounds automatic retries after "prompt is too long".
const maxCompactRetries = 3

// estimateRatios: real/estimated prompt tokens per model, learned from
// "prompt is too long: X > Y" errors (>= 1; 1 when nothing was learned).
var estimateRatios sync.Map

func learnedEstimateRatio(model string) float64 {
	if v, ok := estimateRatios.Load(strings.ToLower(strings.TrimSpace(model))); ok {
		return v.(float64)
	}
	return 1
}

// recordEstimateRatio remembers how much the estimator undercounted a request
// the provider measured exactly. The largest ratio seen is kept (capped).
func recordEstimateRatio(model string, real, estimated int) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || real <= 0 || estimated <= 0 || real <= estimated {
		return
	}
	ratio := min(float64(real)/float64(estimated), 4)
	if ratio > learnedEstimateRatio(model) {
		estimateRatios.Store(model, ratio)
	}
}

// compactInputBudget is the estimated-token budget of one summary request.
func compactInputBudget(window int, ratio float64) int {
	if window <= 0 {
		window = compactUnknownWindow
	}
	if ratio < 1 {
		ratio = 1
	}
	return max(int(float64(window)*compactInputShare/ratio), 2000)
}

func estimateTranscriptTokens(s string) int {
	return llm.EstimateMessageTokens(llm.Message{Role: llm.RoleUser, Content: s})
}

// promptTooLong carries the provider's exact size and limit of a rejected
// request, and our estimate of that request.
type promptTooLong struct {
	real, limit, estimated int
	err                    error
}

func (e *promptTooLong) Error() string { return e.err.Error() }
func (e *promptTooLong) Unwrap() error { return e.err }

// Anthropic: "prompt is too long: 1295213 tokens > 1000000 maximum".
// OpenAI: "maximum context length is 128000 tokens. However, you requested
// 130123 tokens" / "...your messages resulted in 130123 tokens".
var (
	promptTooLongRe    = regexp.MustCompile(`(?i)prompt is too long:?\s*(\d+)\s*tokens?\s*>\s*(\d+)`)
	openAIContextLenRe = regexp.MustCompile(`(?i)maximum context length is (\d+) tokens.*?(?:requested|resulted in) (\d+) tokens`)
)

// parsePromptTooLong returns the real request size and the limit from a
// provider context-length error; ok reports a context-length error at all
// (sizes may be 0 when the message carries no numbers).
func parsePromptTooLong(err error) (real, limit int, ok bool) {
	if err == nil {
		return 0, 0, false
	}
	msg := err.Error()
	if m := promptTooLongRe.FindStringSubmatch(msg); m != nil {
		real, _ = strconv.Atoi(m[1])
		limit, _ = strconv.Atoi(m[2])
		return real, limit, true
	}
	if m := openAIContextLenRe.FindStringSubmatch(msg); m != nil {
		limit, _ = strconv.Atoi(m[1])
		real, _ = strconv.Atoi(m[2])
		return real, limit, true
	}
	return 0, 0, isContextLimitErr(err)
}

type compactNoticeKey struct{}

// withCompactNotice lets the summarizer report a chunked summary.
func withCompactNotice(ctx context.Context, notice func(string)) context.Context {
	return context.WithValue(ctx, compactNoticeKey{}, notice)
}

func compactNotice(ctx context.Context, text string) {
	if f, ok := ctx.Value(compactNoticeKey{}).(func(string)); ok && f != nil {
		f(text)
	}
}

// CompactChunkedNotice is shown when the history was summarized in parts.
// Hosts localize it by this text.
const CompactChunkedNotice = "context compaction: the history is larger than the model window, summarizing it in %d parts"

// summarizeForCompaction summarizes msgs within the summarizing model's
// window (compactWindowFrom(ctx)) and retries smaller when the provider
// still finds the request too long.
func summarizeForCompaction(ctx context.Context, provider llm.Provider, msgs []llm.Message, instruction string) (string, error) {
	window := compactWindowFrom(ctx)
	model := llm.ProviderModelName(provider)
	shrink := 1.0
	for attempt := 0; ; attempt++ {
		budget := max(int(float64(compactInputBudget(window, learnedEstimateRatio(model)))*shrink), 1000)
		summary, err := summarizeWithinBudget(ctx, provider, msgs, instruction, budget)
		if err == nil || ctx.Err() != nil {
			return summary, err
		}
		var tooLong *promptTooLong
		if !errors.As(err, &tooLong) || attempt >= maxCompactRetries {
			return "", err
		}
		if tooLong.real > 0 && tooLong.estimated > 0 {
			recordEstimateRatio(model, tooLong.real, tooLong.estimated)
		}
		if tooLong.real > 0 && tooLong.limit > 0 && tooLong.real > tooLong.limit {
			shrink *= float64(tooLong.limit) / float64(tooLong.real) * 0.85
		} else {
			shrink *= 0.5
		}
	}
}

// summarizeWithinBudget renders msgs so that every summary request stays
// within budget estimated tokens: normal input, tighter caps, then chunks.
func summarizeWithinBudget(ctx context.Context, provider llm.Provider, msgs []llm.Message, instruction string, budget int) (string, error) {
	room := budget - estimateTranscriptTokens(instruction)
	transcript := compactSummaryInput(msgs)
	if estimateTranscriptTokens(transcript) <= room {
		return summarizeOne(ctx, provider, transcript, instruction)
	}
	split := autoCompactSplit(msgs)
	transcript = renderCompactTranscriptCaps(msgs, split, elidedCompactCaps)
	if estimateTranscriptTokens(transcript) <= room {
		return summarizeOne(ctx, provider, transcript, instruction)
	}
	// Rolling summary: each chunk carries the summary of everything before
	// it, so the last answer covers the whole history.
	carry := estimateTranscriptTokens(strings.Repeat("x", compactSummaryMaxChars)) + 200
	chunks := compactChunks(msgs, split, elidedCompactCaps, max(room-carry, 500))
	compactNotice(ctx, fmt.Sprintf(CompactChunkedNotice, len(chunks)))
	summary := ""
	for _, chunk := range chunks {
		input := chunk
		if summary != "" {
			input = "Summary of the earlier part of this conversation:\n" + summary +
				"\n\nThe conversation continues:\n" + chunk
		}
		var err error
		if summary, err = summarizeOne(ctx, provider, input, instruction); err != nil {
			return "", err
		}
	}
	if summary == "" {
		return "", fmt.Errorf("nothing to summarize")
	}
	return summary, nil
}

// summarizeOne sends one request and marks a context-length rejection with
// the provider's numbers and the estimate of what was sent.
func summarizeOne(ctx context.Context, provider llm.Provider, transcript, instruction string) (string, error) {
	summary, err := summarizeTranscript(ctx, provider, transcript, instruction)
	if err == nil || ctx.Err() != nil {
		return summary, err
	}
	if real, limit, ok := parsePromptTooLong(err); ok {
		return "", &promptTooLong{real: real, limit: limit,
			estimated: estimateTranscriptTokens(instruction) + estimateTranscriptTokens(transcript), err: err}
	}
	return "", err
}

// compactChunks renders msgs with caps into consecutive pieces of at most
// budget estimated tokens. A message is never split across pieces; one
// larger than a piece keeps its head and tail.
func compactChunks(msgs []llm.Message, capBefore int, caps compactCaps, budget int) []string {
	var chunks []string
	var b strings.Builder
	used := 0
	flush := func() {
		if b.Len() > 0 {
			chunks = append(chunks, b.String())
			b.Reset()
			used = 0
		}
	}
	for i := range msgs {
		line := renderCompactTranscriptCaps(msgs[i:i+1], capBefore-i, caps)
		if line == "" {
			continue
		}
		cost := estimateTranscriptTokens(line)
		if cost > budget && len(line) > budget*3 {
			line = compactHeadTail(line, budget*3) + "\n"
			cost = estimateTranscriptTokens(line)
		}
		if used > 0 && used+cost > budget {
			flush()
		}
		b.WriteString(line)
		used += cost
	}
	flush()
	return chunks
}
