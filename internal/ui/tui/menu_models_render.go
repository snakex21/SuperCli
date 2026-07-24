package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func isModelMenu(kind menuKind) bool {
	return kind == menuModels || kind == menuModelCatalog || kind == menuProviderModels
}

func isModelVisibilityMenu(kind menuKind) bool {
	return kind == menuModelCatalog || kind == menuProviderModels
}

func (m Model) renderModelsMenu(title, footer string) string {
	rows := m.filteredModelRows()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(fmt.Sprintf("%s · %d", title, len(rows))) + "\n")
	filter := m.menu.filter
	if filter == "" {
		filter = m.tr("start typing", "zacznij pisać")
	}
	b.WriteString(m.palette.InputHint.Render(m.tr("Search: ", "Szukaj: ")+filter) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		available := (m.height - 5) / 2
		start, end = menuWindow(len(rows), m.menu.cursor, available)
	}
	for i := start; i < end; i++ {
		row := rows[i]
		row = m.enrichModelRow(row)
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		state := ""
		if m.menu.kind == menuModels {
			if row.ID == m.reasoningModelName() {
				state = m.tr("[active]", "[aktywny]")
			}
		} else {
			state = "[on]"
			if m.providerMgr != nil && m.providerMgr.IsHiddenFor(row.Provider, row.ID) {
				state = "[off]"
			}
		}
		nameWidth := width - lipgloss.Width(prefix)
		if state != "" {
			nameWidth -= lipgloss.Width(state) + 1
		}
		if nameWidth < 18 {
			nameWidth = 18
		}
		line := prefix + truncateText(row.ID, nameWidth)
		if state != "" {
			line += " " + state
		}
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Bold.Render(line)
		}
		b.WriteString(line + "\n")

		meta := row.Provider + " · ctx " + ctxLen(row.ContextLength) +
			" · in " + m.modelPrice(row, true) + " · out " + m.modelPrice(row, false)
		if c := caps(row); c != "" {
			meta += " · " + c
		}
		if providerState := m.modelProviderState(row.Provider); providerState != "" {
			meta += " · " + providerState
		}
		b.WriteString(m.palette.Dim.Render(truncateText("    "+meta, width)) + "\n")
	}
	if len(rows) == 0 {
		if m.caps != nil && len(m.caps.All()) == 0 {
			b.WriteString("  " + m.tr("scanning providers for models...", "skanowanie modeli dostawców...") + "\n")
		} else {
			b.WriteString("  " + m.tr("no matching models", "brak pasujących modeli") + "\n")
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(footer, width)))
	return b.String()
}

func (m Model) modelProviderState(provider string) string {
	if m.providerMgr != nil && m.providerMgr.IsDisabled(provider) {
		return m.tr("provider paused", "dostawca wstrzymany")
	}
	if status, ok := m.providerStatuses[provider]; ok && status.checked && !status.online {
		return "offline"
	}
	return ""
}

func (m Model) enrichModelRow(row llm.ModelInfo) llm.ModelInfo {
	subscription := m.isSubscriptionProviderName(row.Provider)
	if m.caps != nil {
		if extra, priceSafe, ok := m.lookupModelMetadata(row); ok {
			if row.ContextLength == 0 {
				row.ContextLength = extra.ContextLength
			}
			if priceSafe && !subscription && row.InputCost == 0 {
				row.InputCost = extra.InputCost
			}
			if priceSafe && !subscription && row.OutputCost == 0 {
				row.OutputCost = extra.OutputCost
			}
		}
	}
	if !subscription && (row.InputCost == 0 || row.OutputCost == 0) {
		if rate, key := credits.RateForProvider(row.Provider, row.ID); key != "default" {
			if row.InputCost == 0 {
				row.InputCost = rate.InputPer1k * 1000
			}
			if row.OutputCost == 0 {
				row.OutputCost = rate.OutputPer1k * 1000
			}
		}
	}
	return row
}

func (m Model) lookupModelMetadata(row llm.ModelInfo) (info llm.ModelInfo, priceSafe bool, ok bool) {
	if m.caps == nil || row.ID == "" {
		return llm.ModelInfo{}, false, false
	}
	if row.Provider != "" {
		if extra, ok := m.caps.Get(row.Provider + "/" + row.ID); ok {
			return extra, true, true
		}
	}

	// OpenRouter uses provider-prefixed ids (deepseek/deepseek-v4-flash),
	// while a direct provider often exposes only the short id
	// (deepseek-v4-flash). Use a unique suffix match as metadata only, so
	// non-OpenRouter rows can still display OpenRouter's context_length.
	shortID := modelIDSuffix(row.ID)
	var match llm.ModelInfo
	found := false
	for _, extra := range m.caps.All() {
		if strings.EqualFold(extra.ID, row.ID) || !strings.Contains(extra.ID, "/") {
			continue
		}
		if !strings.EqualFold(modelIDSuffix(extra.ID), shortID) {
			continue
		}
		if found {
			return llm.ModelInfo{}, false, false
		}
		match = extra
		found = true
	}
	return match, false, found
}

func modelIDSuffix(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}

func (m Model) modelPrice(row llm.ModelInfo, input bool) string {
	if m.isSubscriptionProviderName(row.Provider) {
		return "sub"
	}
	if input {
		return price(row.InputCost)
	}
	return price(row.OutputCost)
}

func (m Model) isSubscriptionProviderName(name string) bool {
	if strings.EqualFold(name, config.ProviderCodex) {
		return true
	}
	if m.providerMgr == nil || name == "" {
		return false
	}
	for _, p := range m.providerMgr.Configured() {
		if p.Name == name && p.Type == config.ProviderCodex {
			return true
		}
	}
	return false
}
