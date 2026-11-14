package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/tools"
)

// handleSlashThunderbird pairs Thunderbird AI Bridge with this SuperCli data
// folder: /thunderbird [status|pair|unpair|cancel]. The pairing code is shown
// once in the transcript and is valid for five minutes.
func (m Model) handleSlashThunderbird(cmd SlashCommand) (tea.Model, tea.Cmd) {
	dm := m.marker
	dataDir := m.dataDir
	tr := m.tr
	action := strings.ToLower(strings.TrimSpace(cmd.Args))
	return m, func() tea.Msg {
		switch action {
		case "", "status":
			st := tools.ThunderbirdBridgeStatusFor(dataDir)
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(tr("tui.thunderbird.status"), st.Message))}
		case "pair", "repair", "re-pair":
			code, err := tools.StartThunderbirdPairing(dataDir)
			if err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(fmt.Sprintf(tr("tui.thunderbird.code"), code.Code, code.ExpiresAt.Local().Format("15:04")))}
		case "unpair":
			if err := tools.UnpairThunderbird(dataDir); err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(tr("tui.thunderbird.unpaired"))}
		case "cancel":
			if err := tools.CancelThunderbirdPairing(dataDir); err != nil {
				return slashResultMsg{Err: err}
			}
			return slashResultMsg{Body: dm.Diff(tr("tui.thunderbird.cancelled"))}
		default:
			return slashResultMsg{Body: dm.Diff(tr("tui.thunderbird.usage"))}
		}
	}
}
