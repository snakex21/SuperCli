package webgui

import (
	"context"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
)

// webLiveContextBlocks captures only immutable scope identifiers. It never
// resolves the engine's currently selected session/root during an active run.
func (e *Engine) webLiveContextBlocks(home string, tokenCap int, sessionID string, goals *goal.Service, office bool) func(context.Context) ([]llm.ContextBlock, error) {
	return e.webLiveContextBlocksMemory(home, tokenCap, sessionID, goals, office, true)
}

// webLiveContextBlocksMemory is webLiveContextBlocks with the memory switch.
// With memory off the memory block stays in place but empty, so the block
// layout is unchanged and no memory store is opened or read.
func (e *Engine) webLiveContextBlocksMemory(home string, tokenCap int, sessionID string, goals *goal.Service, office, memoryOn bool) func(context.Context) ([]llm.ContextBlock, error) {
	return func(ctx context.Context) ([]llm.ContextBlock, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		briefing := ""
		if memoryOn {
			briefing = e.webMemoryBriefingExcludingSession(home, tokenCap, sessionID)
		}
		blocks := []llm.ContextBlock{
			{Source: llm.SourceMemory, Text: briefing},
			{Source: llm.SourceRuntime, Text: folderIndexPrompt(e.dataDir)},
		}
		if goals != nil {
			if _, err := goals.Refresh(ctx); err != nil {
				return nil, err
			}
			current, err := goals.Inject(ctx, "", 5)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, llm.ContextBlock{Source: llm.SourceGoalState, Text: current})
		}
		// The real sandbox root wins over any path mentioned by memory facts.
		blocks = append(blocks, llm.ContextBlock{Source: llm.SourceRuntime, Text: webWorkspaceNote(home, office)})
		return blocks, ctx.Err()
	}
}
