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
		m.appendLine(m.marker.Error(fmt.Errorf("shell escape: runner not configured")))
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
func renderProvidersList(mgr *providers.Manager, caps *llm.CapabilityRegistry) string {
	var b strings.Builder
	infos := mgr.List(caps)
	if len(infos) == 0 {
		return "No providers configured.\n\nAdd one:\n  /providers add <name> <type> <base_url> [api_key]\n\nTypes: openai, anthropic, codex, opencode, echo"
	}
	for _, pi := range infos {
		status := "✗ disconnected"
		if pi.Connected {
			status = "✓ connected"
		}
		modelCount := len(pi.Models)
		fmt.Fprintf(&b, "%s (%s) — %s | %d model(s)\n",
			pi.Name, pi.Type, status, modelCount)
		if pi.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", pi.Error)
		}
		if pi.BaseURL != "" {
			fmt.Fprintf(&b, "  base_url: %s\n", pi.BaseURL)
		}
	}
	b.WriteString("\nSubcommands:\n")
	b.WriteString("  /providers add <name> <type> <url> [key]\n")
	b.WriteString("  /providers remove <name>\n")
	b.WriteString("  /providers price <model> <in> <out>\n")
	b.WriteString("  /providers toggle <model>\n")
	return b.String()
}
