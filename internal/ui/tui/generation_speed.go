package tui

import (
	"fmt"
)

// Completion rates come from DoneEvent, never inferred from visible text or
// recomputed on streaming deltas. The completion row keeps its measured value
// so a presentation preference can change without touching the conversation.
func (c *chat) addCompletion(text string, speed float64) {
	c.msgs = append(c.msgs, msg{role: roleSystem, text: text, generationSpeed: speed})
	c.completedDirty = true
	c.invalidateTranscriptSearch()
}

func generationSpeedSuffix(speed float64, p Palette, language string) string {
	return p.Dim.Render(" · " + fmt.Sprintf(textFor(language, "generation.rate"), speed))
}

// Called only after a successful portable settings save. Refreshing here makes
// existing completion rows follow the preference when the settings menu closes.
func (m *Model) applyGenerationSpeedPreference(value *bool) {
	hidden := value != nil && !*value
	if m.chat.hideGenerationSpeed == hidden {
		return
	}
	m.chat.hideGenerationSpeed = hidden
	m.chat.completedDirty = true
	m.refreshTranscript()
}
