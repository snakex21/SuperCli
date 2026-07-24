package tui

import (
	"fmt"
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
	width := m.menuWidth()
	sel := rows[minInt(m.menu.cursor, len(rows)-1)]
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(truncateVisible(m.tr("Settings · ", "Ustawienia · ")+m.localizedSettingSection(sel.key), width)) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Enter changes · R restores defaults · (next session) after restart", "Enter zmienia · R przywraca domyślne · (następna sesja) po ponownym uruchomieniu"), width)) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		start, end = menuWindow(len(rows), m.menu.cursor, m.height-7)
	}
	for i := start; i < end; i++ {
		r := rows[i]
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		if r.kind == setResetAll {
			line := r.label
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(line)
			} else {
				line = m.palette.Bold.Render(line)
			}
			b.WriteString("\n" + prefix + line + "\n")
			continue
		}
		value, source := m.settingValueSource(r, cfg)
		value, source = m.localizeSettingDisplay(value, source)
		if (r.kind == setInt || r.kind == setText) && m.menu.editing && i == m.menu.cursor {
			value = m.menu.editBuf + "_"
			source = "editing"
		}
		marker := ""
		if r.nextSession {
			marker = m.tr(" (next session)", " (następna sesja)")
		}
		labelWidth := 28
		valueWidth := 22
		if width < 72 {
			labelWidth = maxInt(12, width/3)
			valueWidth = maxInt(10, width-labelWidth-18)
		}
		head := fmt.Sprintf("%-*s %-*s", labelWidth, truncateText(r.label, labelWidth), valueWidth, truncateText(value, valueWidth))
		line := truncateText(prefix+head+" ["+source+"] · "+r.key+marker, width)
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Dim.Render(line)
		}
		b.WriteString(line + "\n")
	}
	detail := sel.desc
	if sel.key != "" {
		detail = sel.key + " · " + detail
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(detail, width)) + "\n")
	footer := m.tr("↑↓ select · Enter change · R reset · Esc back", "↑↓ wybierz · Enter zmień · R resetuj · Esc wróć")
	if m.menu.editing {
		footer = m.tr("type digits · Enter save · Backspace delete · Esc cancel", "wpisz cyfry · Enter zapisz · Backspace usuń · Esc anuluj")
		if sel.kind == setText {
			footer = m.tr("type text · Enter save · Backspace delete · Esc cancel (empty = default)", "wpisz tekst · Enter zapisz · Backspace usuń · Esc anuluj (puste = domyślne)")
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(footer, width)))
	return b.String()
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
