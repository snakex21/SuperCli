package tui

import (
	"strings"

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
	page := menuPage{title: title, searchable: true, footer: footer,
		empty: m.tr("No matching models.", "Brak pasujących modeli.")}
	if m.modelPickerScanning {
		page.empty = m.tr("scanning providers for models…", "Wykrywanie modeli dostawców…")
	}
	for i, row := range rows {
		state := ""
		if m.menu.kind == menuModels {
			if row.ID == m.reasoningModelName() && (m.activeProvider == "" || row.Provider == m.activeProvider) {
				state = m.tr("● active", "● aktywny")
			}
		} else {
			state = m.tr("[on]", "[włączony]")
			if m.providerMgr != nil && m.providerMgr.IsHiddenFor(row.Provider, row.ID) {
				state = m.tr("[off]", "[wyłączony]")
			}
		}
		meta := row.Provider
		if row.ContextLength > 0 {
			meta += " · ctx " + ctxLen(row.ContextLength)
		}
		if providerState := m.modelProviderState(row.Provider); providerState != "" {
			meta += " · " + providerState
		}
		page.items = append(page.items, menuListItem{label: row.ID, meta: meta, badge: state})
		if i == m.menu.cursor {
			row = m.enrichModelRow(row)
			page.detailTitle = row.ID
			page.detail = []string{m.tr("Provider: ", "Dostawca: ") + row.Provider,
				m.tr("Context: ", "Kontekst: ") + ctxLen(row.ContextLength), "",
				"in " + m.modelPrice(row, true) + " · out " + m.modelPrice(row, false), caps(row)}
		}
	}
	return m.renderMenuPage(page)
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
