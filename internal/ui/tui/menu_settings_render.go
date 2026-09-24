package tui

import (
	"strings"

	"supercli/internal/system/config"
)

func (m Model) renderSettingsMenu() string {
	cfg := m.menu.settingsCfg
	if cfg == nil {
		c, _ := config.LoadToml(m.settingsGlobalPath())
		cfg = &c
	}
	rows := m.localizedSettingsRows()
	page := menuPage{title: m.tr("Settings", "Ustawienia"), tabs: m.menuTabs(m.settingsCategories(), m.menu.category),
		footer: m.tr("↑↓ choose · ←→ category · Enter change · R reset", "↑↓ wybierz · ←→ kategoria · Enter zmień · R reset")}
	for i, row := range rows {
		value, source := m.settingValueSource(row, cfg)
		value, source = m.localizeSettingDisplay(value, source)
		if row.kind == setResetAll {
			value = ""
		}
		if m.menu.editing && i == m.menu.cursor {
			value = m.menu.editBuf + "▏"
		}
		badge := value
		if strings.HasPrefix(value, "default (") || strings.HasPrefix(value, "domyśln") {
			badge = m.tr("default", "domyślne")
		}
		page.items = append(page.items, menuListItem{label: row.label, badge: badge})
		if i == m.menu.cursor {
			page.detailTitle = row.label
			page.detail = []string{row.desc}
			if row.key != "" {
				page.detail = append(page.detail, "", m.tr("Value: ", "Wartość: ")+value, m.tr("Source: ", "Źródło: ")+source, "", row.key)
			}
			if row.nextSession {
				page.detail = append(page.detail, "", m.tr("Applies after restart (next session).", "Zadziała po restarcie (następna sesja)."))
			}
		}
	}
	if m.menu.editing {
		page.footer = m.tr("Type value · Enter save · Esc cancel", "Wpisz wartość · Enter zapisz · Esc anuluj")
	}
	if m.menu.formErr != "" {
		page.detail = append([]string{m.menu.formErr, ""}, page.detail...)
	}
	return m.renderMenuPage(page)
}

func (m Model) localizeSettingDisplay(value, source string) (string, string) {
	if m.language != "pl" {
		if value == "zawsze" {
			value = "always"
		} else if value == "nigdy" {
			value = "never"
		}
		return value, source
	}
	replacements := map[string]string{
		"default (main model)":          "domyślny (model główny)",
		"default (active model)":        "domyślny (aktywny model)",
		"default (spec or 10)":          "domyślnie (profil lub 10)",
		"default (no cap)":              "domyślnie (bez limitu)",
		"default (scaled)":              "domyślnie (skalowane)",
		"default (700/300 by tier)":     "domyślnie (700/300 wg profilu)",
		"English":                       "Angielski",
		"on":                            "włączone",
		"off":                           "wyłączone",
		"parallel":                      "równolegle",
		"sequential":                    "sekwencyjnie",
		"none (diff-only verdict)":      "brak (tylko ocena zmian)",
		"off (no paid fallback)":        "wyłączone (bez płatnego zapasu)",
		"default (coordinator's model)": "domyślny (model koordynatora)",
		"prune 60% · compact window − reserve": "skracanie 60% · kompakcja: okno − rezerwa",
	}
	if translated, ok := replacements[value]; ok {
		value = translated
	}
	if strings.HasPrefix(value, "default (") {
		value = "domyślnie (" + strings.TrimPrefix(value, "default (")
	}
	switch source {
	case "default":
		source = "domyślne"
	case "manual":
		source = "własne"
	case "built-in":
		source = "wbudowane"
	case "editing":
		source = "edycja"
	case "set via /model":
		source = "ustawiane w modelach"
	case "set via /providers":
		source = "ustawiane u dostawców"
	}
	return value, source
}
