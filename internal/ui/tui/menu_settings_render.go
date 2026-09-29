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
	page := menuPage{title: m.tr("tui.menu_navigation.74a883a037"), tabs: m.menuTabs(m.settingsCategories(), m.menu.category),
		footer: m.tr("tui.menu_settings_render.5dbfc12d13")}
	for i, row := range rows {
		value, source := m.settingValueSource(row, cfg)
		defaultValue := source == "default"
		source = m.localizeSettingSource(source)
		if row.kind == setResetAll {
			value = ""
		}
		if m.menu.editing && i == m.menu.cursor {
			value = m.menu.editBuf + "▏"
		}
		badge := value
		if defaultValue && strings.HasSuffix(value, ")") {
			badge = m.tr("tui.menu_settings_render.37a8eec1ce")
		}
		page.items = append(page.items, menuListItem{label: row.label, badge: badge})
		if i == m.menu.cursor {
			page.detailTitle = row.label
			page.detail = []string{row.desc}
			if row.key != "" {
				page.detail = append(page.detail, "", m.tr("tui.menu_settings_render.905b8b1136")+value, m.tr("tui.menu_settings_render.1a5ac0bd0b")+source, "", row.key)
			}
			if row.nextSession {
				page.detail = append(page.detail, "", m.tr("tui.menu_settings_render.5858764be7"))
			}
		}
	}
	if m.menu.editing {
		page.footer = m.tr("tui.menu_settings_render.c9366e8025")
	}
	if m.menu.formErr != "" {
		page.detail = append([]string{m.menu.formErr, ""}, page.detail...)
	}
	return m.renderMenuPage(page)
}

func (m Model) localizeSettingSource(source string) string {
	switch source {
	case "default":
		source = m.tr("tui.menu_settings_render.37a8eec1ce")
	case "manual":
		source = m.tr("tui.view_markers.36bde66f28")
	case "built-in":
		source = m.tr("tui.setting_source.5c73a5c73d")
	case "editing":
		source = m.tr("tui.setting_source.62b8e80d98")
	case "set via /model":
		source = m.tr("tui.setting_source.fc0b78c9c0")
	case "set via /providers":
		source = m.tr("tui.setting_source.1aace6fff5")
	}
	return source
}
