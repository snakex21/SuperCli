// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/doctor"
	"supercli/internal/tools/shellescape"
)

// shellResultMsg is delivered when a !command finishes.
type shellResultMsg struct {
	res *shellescape.Result
}

// doctorReportMsg delivers an asynchronously computed /doctor report.
type doctorReportMsg struct {
	report *doctor.Report
}

// modelSwapRequestMsg is emitted by /model to request a provider swap.
type modelSwapRequestMsg struct {
	ModelID  string
	Provider string // provider name that owns this model
}

// dispatchShellEscape runs a !command in a goroutine and
// returns a tea.Cmd that emits a shellResultMsg.
func (m Model) dispatchShellEscape(text string) (tea.Model, tea.Cmd) {
	if m.shellRunner == nil {
		m.appendLine(m.marker.Error(fmt.Errorf("%s", m.tr("tui.other.c116f8a729"))))
		m.refreshTranscript()
		return m, nil
	}
	cmd := shellescape.ExtractCommand(text)
	m.chat.addUser("> !" + cmd)
	m.appendLineToTranscript("> !" + cmd)
	m.appendLine(m.marker.Running())
	m.refreshTranscript()
	m.busy = true
	return m, func() tea.Msg {
		res := m.shellRunner.Run(context.Background(), cmd)
		return shellResultMsg{res: res}
	}
}

// writeExportFile writes exported content to a file.
func writeExportFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// renderProvidersList renders the provider list with connectivity
// status, model counts, and visibility indicators.
func renderProvidersList(mgr *providers.Manager, caps *llm.CapabilityRegistry, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = languages[0]
	}
	tr := func(key string) string { return textFor(language, key) }
	var b strings.Builder
	infos := mgr.List(caps)
	if len(infos) == 0 {
		return tr("tui.other.b89ccec37a")
	}
	for _, pi := range infos {
		status := tr("tui.other.bf10f597cc")
		if pi.Connected {
			status = tr("tui.other.225c9957ac")
		}
		modelCount := len(pi.Models)
		fmt.Fprintf(&b, tr("tui.other.04d53f5dcb"),
			pi.Name, pi.Type, status, modelCount)
		if pi.Error != "" {
			fmt.Fprintf(&b, tr("tui.other.f053e079ad"), pi.Error)
		}
		if pi.BaseURL != "" {
			fmt.Fprintf(&b, "  base_url: %s\n", pi.BaseURL)
		}
	}
	b.WriteString("\n" + tr("tui.providers.subcommands") + "\n")
	b.WriteString("  /providers add <name> <type> <url> [key]\n")
	b.WriteString("  /providers remove <name>\n")
	b.WriteString("  /providers price <model> <in> <out>\n")
	b.WriteString("  /providers toggle <model>\n")
	return b.String()
}
