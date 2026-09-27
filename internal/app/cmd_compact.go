package app

import "supercli/internal/agent"
import "supercli/internal/llm"

// Memory autosave uses the same bounded transcript as compaction.
// Manual /compact runs through Loop.CompactNow, like the GUI.
func renderCompactTranscript(msgs []llm.Message) string {
	return agent.RenderCompactTranscript(msgs)
}
