package prompt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const UserInstructionsFile = "user-instructions.json"

// UserInstructionPreset is one reusable set of instructions written by the
// user. Content is deliberately not capped: the UI reports the approximate
// context cost and lets the user make that trade-off explicitly.
type UserInstructionPreset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// UserInstructionsState is installation-local. SuperCli and branded apps keep
// separate copies because each executable resolves its own data directory.
type UserInstructionsState struct {
	Version  int                     `json:"version"`
	Enabled  bool                    `json:"enabled"`
	ActiveID string                  `json:"active_id"`
	Presets  []UserInstructionPreset `json:"presets"`
}

func UserInstructionsPath(dataDir string) string {
	return filepath.Join(dataDir, UserInstructionsFile)
}

func LoadUserInstructions(dataDir string) (UserInstructionsState, error) {
	state := UserInstructionsState{Version: 1, Presets: []UserInstructionPreset{}}
	b, err := os.ReadFile(UserInstructionsPath(dataDir))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return UserInstructionsState{}, fmt.Errorf("read user instructions: %w", err)
	}
	return NormalizeUserInstructions(state), nil
}

func SaveUserInstructions(dataDir string, state UserInstructionsState) (UserInstructionsState, error) {
	state = NormalizeUserInstructions(state)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return UserInstructionsState{}, err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return UserInstructionsState{}, err
	}
	path := UserInstructionsPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return UserInstructionsState{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return UserInstructionsState{}, err
	}
	return state, nil
}

func NormalizeUserInstructions(state UserInstructionsState) UserInstructionsState {
	state.Version = 1
	seen := make(map[string]bool, len(state.Presets))
	clean := make([]UserInstructionPreset, 0, len(state.Presets))
	for i, preset := range state.Presets {
		id := strings.TrimSpace(preset.ID)
		if id == "" || seen[id] {
			id = fmt.Sprintf("preset-%d", i+1)
			for seen[id] {
				id += "-copy"
			}
		}
		seen[id] = true
		name := strings.TrimSpace(preset.Name)
		if name == "" {
			name = fmt.Sprintf("Preset %d", len(clean)+1)
		}
		clean = append(clean, UserInstructionPreset{ID: id, Name: name, Content: preset.Content})
	}
	state.Presets = clean
	if !seen[state.ActiveID] {
		state.ActiveID = ""
		if len(clean) > 0 {
			state.ActiveID = clean[0].ID
		}
	}
	if state.ActiveID == "" {
		state.Enabled = false
	}
	return state
}

// ActiveUserInstructions returns the stable prompt block for the enabled
// preset. Reading the local JSON file does not call a model; identical content
// also preserves provider-side prompt caching.
func ActiveUserInstructions(dataDir string) string {
	state, err := LoadUserInstructions(dataDir)
	if err != nil || !state.Enabled {
		return ""
	}
	for _, preset := range state.Presets {
		if preset.ID != state.ActiveID || strings.TrimSpace(preset.Content) == "" {
			continue
		}
		return "User instructions (enabled preset: " + preset.Name + "). Apply these preferences unless the current user request conflicts; the current request, safety rules, and tool rules take priority:\n" + strings.TrimSpace(preset.Content)
	}
	return ""
}

func EstimateInstructionTokens(content string) int {
	if content == "" {
		return 0
	}
	// A transparent UI estimate, not a billing number. It is intentionally
	// conservative for mixed Polish/English prose and requires no tokenizer.
	return (utf8.RuneCountInString(content) + 2) / 3
}
