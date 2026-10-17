package webgui

import (
	"path/filepath"

	"supercli/internal/storage/memory"
)

// Capture immutable project metadata before any project switch. All subscribers
// receive these summaries; transcript events remain scoped to the selected chat.
func (e *Engine) syncRunDetails(sessionID, home, prompt string) syncFrame {
	info := syncFrame{ProjectPath: home, ProjectName: filepath.Base(home), Title: summarizeHistoryMessage(prompt, sessionTitleMaxRunes)}
	if ws := memory.LoadWorkspace(e.dataDir); ws != nil {
		if project, ok := ws.Get(home); ok {
			info.ProjectName = project.Name
		}
	}
	if store, err := e.sessionStore(); err == nil {
		if session, err := store.Get(sessionID); err == nil && session.Title != "" {
			info.Title = session.Title
		}
	}
	return info
}
