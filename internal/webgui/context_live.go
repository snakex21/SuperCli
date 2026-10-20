package webgui

import (
	"context"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
)

// webLiveContextBlocks captures only immutable scope identifiers. It never
// resolves the engine's currently selected session/root during an active run.
func (e *Engine) webLiveContextBlocks(home string, tokenCap int, sessionID string, goals *goal.Service, office bool) func(context.Context) ([]llm.ContextBlock, error) {
	return func(ctx context.Context) ([]llm.ContextBlock, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		blocks := []llm.ContextBlock{
			{Source: llm.SourceMemory, Text: e.webMemoryBriefingExcludingSession(home, tokenCap, sessionID)},
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
