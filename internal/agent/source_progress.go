package agent

import (
	"crypto/sha256"
	"fmt"

	"supercli/internal/llm"
)

type sourceRound struct{ fresh, reused int }
type sourceProgress struct {
	seen          map[sourceLocation][sha256.Size]byte
	recent        []sourceRound
	unchanged     int
	warn          bool
	fresh, reused int
}

// Query/range variation does not turn known source into new evidence. A small
// amount of genuinely new code is allowed: mostly repeated reads only get advice.
// Stop only consecutive rounds that contain no new evidence at all.
func (p *sourceProgress) observe(calls []llm.ToolCall, outcomes []callOutcome) {
	p.warn, p.fresh, p.reused = false, 0, 0
	allSource := len(calls) > 0
	for i, call := range calls {
		call = sourceCall(call)
		outcome := outcomeAt(outcomes, i)
		if outcome.failed || !observationReadCall(call) {
			*p = sourceProgress{}
			allSource = false
			continue
		}
		rows := outcome.source
		if outcome.sourcePartial {
			allSource = false
		}
		if len(rows) == 0 {
			allSource = false
			continue
		}
		if p.seen == nil {
			p.seen = make(map[sourceLocation][sha256.Size]byte)
		}
		for _, row := range rows {
			if previous, exists := p.seen[row.location]; exists && previous == row.hash {
				p.reused++
			} else {
				p.fresh++
				if len(p.seen) >= sourceEvidenceRows {
					clear(p.seen)
				}
				p.seen[row.location] = row.hash
			}
		}
	}
	if allSource && p.reused > 0 && p.fresh == 0 {
		p.unchanged++
	} else {
		p.unchanged = 0
	}
	if p.fresh+p.reused == 0 {
		return
	}
	p.recent = append(p.recent, sourceRound{p.fresh, p.reused})
	if len(p.recent) > 8 {
		p.recent = p.recent[len(p.recent)-8:]
	}
	fresh, reused := 0, 0
	for _, round := range p.recent {
		fresh += round.fresh
		reused += round.reused
	}
	p.warn = p.unchanged >= 3 || (len(p.recent) >= 6 && reused >= 48 && fresh*3 <= fresh+reused)
}

func (p *repeatProgress) syncSourceEpoch(epoch uint64) {
	if p.sourceEpoch != epoch {
		p.source = sourceProgress{}
		p.sourceEpoch = epoch
	}
}

func (p *sourceProgress) warningText() string {
	return fmt.Sprintf("[loop] Source exploration is revisiting known code despite changed queries/ranges (%d repeated, %d new lines in this round). Use the findings to make the requested change or answer the investigation. Search again only for a specific missing fact, changed source or failed validation. Do not promise edits followed by another broad search. For read-only work, report the findings instead of editing.", p.reused, p.fresh)
}
