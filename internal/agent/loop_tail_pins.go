package agent

import (
	"crypto/sha256"
	"strings"
	"time"

	"supercli/internal/llm"
)

// Request-only trailing context (freshness stamp, live context, run hints)
// used to be rebuilt behind the conversation on EVERY request. Hosted
// prompt caches (OpenAI Responses, Anthropic) only reuse a prefix that a
// previous request actually ended with, so a tail that moves one slot to
// the right each step meant no request was ever a prefix of the next one:
// measured on gpt-6-luna, every call re-read the whole history and only the
// system/tool block hit the cache. The first user message also changed
// between step 1 and step 2 because the demoted tail was merged into it.
//
// The pinned tail keeps requests append-only instead. A tail is sent once
// and then stays at the position where it was first sent for as long as its
// content (ignoring the minute stamp) is unchanged. When it changes it MOVES
// to the end of the request: every request still carries exactly one tail,
// so a refreshed snapshot or a new turn's instructions never leave a stale
// copy behind. The pinned copy never enters l.Messages, so persistence,
// pruning and compaction never see it; a history rewrite before the pin
// drops it.
type tailPin struct {
	pos    int      // index in the provider message list where the tail sits
	anchor [32]byte // digest of the message right before pos, detects rewrites
	msgs   []llm.Message
	key    string // tail text without the minute stamp
	at     time.Time
	tokens int
}

// tailRestampAfter bounds how stale the pinned date/time may get before a
// changed minute alone moves the tail.
const tailRestampAfter = 30 * time.Minute

func tailPinDigest(m llm.Message) [32]byte {
	h := sha256.New()
	h.Write([]byte(m.Role))
	h.Write([]byte{0})
	h.Write([]byte(m.Content))
	h.Write([]byte{0})
	h.Write([]byte(m.ToolCallID))
	for _, part := range m.Parts {
		h.Write([]byte{1})
		h.Write([]byte(part.Type))
		h.Write([]byte(part.Text))
	}
	for _, tc := range m.ToolCalls {
		h.Write([]byte{0})
		h.Write([]byte(tc.ID))
		h.Write([]byte(tc.Name))
		h.Write([]byte(tc.Arguments))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// tailPinKey is the trailing group's text without the per-minute stamp, so a
// clock tick alone does not move the tail.
func tailPinKey(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteByte(0)
		for _, line := range strings.Split(m.Content, "\n") {
			if strings.HasPrefix(line, "Current local date/time:") {
				continue
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// resetTailPins forgets the pinned tail; the next request sends its tail at
// the end again. Called whenever the history is rewritten.
func (l *Loop) resetTailPins() { l.tailPin = nil }

// applyTailPins returns conv plus the request-only trailing group: at its
// pinned position when the pin is still valid and its content unchanged,
// otherwise at the end. It also returns the token estimate of what it
// inserted. commit records the pin (real provider requests only); a preview
// render (commit=false) shows what the next request would send without
// changing state.
func (l *Loop) applyTailPins(conv []llm.Message, trailing []llm.Message, commit bool) ([]llm.Message, int) {
	if l.tailReplace || len(trailing) == 0 || len(conv) == 0 {
		tokens := 0
		for _, m := range trailing {
			tokens += llm.EstimateMessageTokens(m)
		}
		return append(conv, trailing...), tokens
	}
	now := time.Now()
	key := tailPinKey(trailing)
	if p := l.tailPin; p != nil && p.pos <= len(conv) && p.pos > 0 && tailPinDigest(conv[p.pos-1]) == p.anchor &&
		p.key == key && now.Sub(p.at) < tailRestampAfter {
		out := make([]llm.Message, 0, len(conv)+len(p.msgs))
		out = append(out, conv[:p.pos]...)
		out = append(out, p.msgs...)
		out = append(out, conv[p.pos:]...)
		return out, p.tokens
	}
	tokens := 0
	for _, m := range trailing {
		tokens += llm.EstimateMessageTokens(m)
	}
	if commit {
		l.tailPin = &tailPin{
			pos: len(conv), anchor: tailPinDigest(conv[len(conv)-1]),
			msgs: append([]llm.Message(nil), trailing...), key: key, at: now, tokens: tokens,
		}
	}
	return append(conv, trailing...), tokens
}
