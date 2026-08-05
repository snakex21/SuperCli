package webgui

import (
	"encoding/json"
	"net/http"
	"strings"

	llmprompt "supercli/internal/llm/prompt"
)

type userInstructionsView struct {
	llmprompt.UserInstructionsState
	Path            string `json:"path"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Applied         bool   `json:"applied"`
}

func (s *Server) handleUserInstructions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var (
		state llmprompt.UserInstructionsState
		err   error
	)
	if r.Method == http.MethodPost {
		if err = json.NewDecoder(r.Body).Decode(&state); err == nil {
			state, err = llmprompt.SaveUserInstructions(s.eng.DataDir(), state)
		}
	} else {
		state, err = llmprompt.LoadUserInstructions(s.eng.DataDir())
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	estimate := 0
	applied := false
	for _, preset := range state.Presets {
		if preset.ID == state.ActiveID {
			estimate = llmprompt.EstimateInstructionTokens(preset.Content)
			applied = state.Enabled && strings.TrimSpace(preset.Content) != ""
			break
		}
	}
	writeJSON(w, userInstructionsView{
		UserInstructionsState: state,
		Path:                  llmprompt.UserInstructionsPath(s.eng.DataDir()),
		EstimatedTokens:       estimate,
		Applied:               applied,
	})
}
