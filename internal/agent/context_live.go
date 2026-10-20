package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// RefreshLiveContext prepares request-only state for idle inspections and
// manual compaction, which do not call Run. It uses the same ownership guard
// as Run so a caller cannot replace state while a provider owns its request.
func (l *Loop) RefreshLiveContext(ctx context.Context) error {
	if !l.sessionBusy.CompareAndSwap(false, true) {
		return fmt.Errorf("agent is still finishing the previous run")
	}
	defer l.releaseConversation()
	return l.refreshLiveContextBoundary(ctx, true)
}

// refreshLiveContextBoundary replaces snapshots, not conversation messages.
// Callers hold the conversation lease and have completed the previous tool
// batch. Ordinary reads leave the snapshot intact; source changes are coalesced
// into one refresh before the next provider request. Rebuilding every run also
// handles resume, compaction and changes made outside the model's own tools.
func (l *Loop) refreshLiveContextBoundary(ctx context.Context, force bool) error {
	if force && l.liveContextBlocksForRun == nil && l.liveContextForRun != nil {
		text, err := l.liveContextForRun(ctx)
		if err != nil {
			return err
		}
		l.liveContext = strings.TrimSpace(text)
	}
	if l.liveContextBlocksForRun == nil || (!force && !l.liveContextDirty) {
		return nil
	}
	blocks, err := l.liveContextBlocksForRun(ctx)
	if err != nil {
		return err
	}
	// Own the small slice: a host callback may reuse its scratch buffer. Old
	// request snapshots must remain unchanged after a later refresh.
	current := make([]llm.ContextBlock, 0, len(blocks))
	for _, block := range blocks {
		block.Text = strings.TrimSpace(block.Text)
		if block.Text != "" {
			current = append(current, block)
		}
	}
	epoch := l.conversationEpoch.Load()
	if l.stats != nil {
		previous := make(map[llm.ContextBlock]int, len(l.liveContextBlocks))
		if l.liveContextEpoch == epoch {
			for _, block := range l.liveContextBlocks {
				previous[block]++
			}
		}
		for _, block := range current {
			if previous[block] > 0 {
				previous[block]--
				continue
			}
			l.recordContextAddition(llm.MessageWithContextBlocks(llm.RoleSystem, []llm.ContextBlock{block}))
		}
	}
	l.liveContextBlocks = current
	l.liveContext = "" // the typed callback replaces the legacy snapshot
	l.liveContextEpoch = epoch
	l.liveContextDirty = false
	return nil
}

func (l *Loop) markLiveContextAfterTools(calls []llm.ToolCall, outcomes []callOutcome) {
	if l.liveContextBlocksForRun == nil {
		return
	}
	for i, call := range calls {
		if i >= len(outcomes) || outcomes[i].inert {
			continue
		}
		switch call.Name {
		case "remember", "task", "send_message":
			// A failed memory mirror or delegated run may still have committed
			// partial durable state. Refresh even then; a report is not proof
			// that the previous snapshot is still current.
			l.liveContextDirty = true
		case "goal":
			var args struct {
				Action string `json:"action"`
			}
			if json.Unmarshal([]byte(call.Arguments), &args) == nil {
				switch args.Action {
				case "set", "add_task", "start_task", "complete_task", "skip_task", "add_note", "verify", "mark_done", "abandon", "decompose":
					l.liveContextDirty = true
				}
			}
		}
	}
}
