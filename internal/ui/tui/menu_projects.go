package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/storage/memory"
)

// menu_projects.go is the interactive front-end for the per-project
// memory map owned by internal/storage/memory/projects.go and the
// /projects slash command. The menu never duplicates storage logic —
// it reads projects.json via memory.LoadProjectsMap and triggers the
// existing slash handler, so there is one source of truth.
//
// Open with:  /projects  (no args)  ·  Ctrl+P  ·  the 'p' shortcut
//
// Keys:
//   ↑↓   navigate
//   Enter  info on the highlighted project (or "add current directory" on the + row)
//   a      add the current working directory
//   d      remove the highlighted project (memory stays on disk)
//   ESC    back

// projectRow is one line in the projects menu: either a registered
// project (with its key + memory size) or the trailing "+ add"
// action row.
type projectRow struct {
	path  string // absolute path; "" for the add-action row
	key   string // ProjectKey(path)
	isCwd bool   // true if this row matches the cwd
	isAdd bool   // true for the trailing "+ add" row
}

// projectRows builds the menu rows: every registered project first
// (sorted by path), then an "+ add current directory" action row.
func (m Model) projectRows() []projectRow {
	if m.dataDir == "" {
		// No data dir → no project memory at all. Still show the
		// add row so the user sees something instead of an empty
		// panel.
		return []projectRow{{isAdd: true}}
	}
	projMap := memory.LoadProjectsMap(m.dataDir)
	cwd, _ := os.Getwd()

	paths := make([]string, 0, len(projMap))
	for p := range projMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	rows := make([]projectRow, 0, len(paths)+1)
	for _, p := range paths {
		rows = append(rows, projectRow{path: p, key: projMap[p], isCwd: p == cwd})
	}
	rows = append(rows, projectRow{isAdd: true})
	return rows
}

// openProjectsMenu opens the interactive projects menu, mirroring
// openModelsMenu / openProvidersMenu.
func (m Model) openProjectsMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuProjects, cursor: 0}
	m.input.Blur()
	return m, nil
}

// projectsMenuEnter handles Enter in the projects menu:
//   - on the add-action row: register the current directory
//   - on a project row: run /projects info <path> for that project
func (m Model) projectsMenuEnter() (tea.Model, tea.Cmd) {
	rows := m.projectRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[minInt(m.menu.cursor, len(rows)-1)]
	next, _ := m.closeMenu()
	mm := next.(Model)
	if row.isAdd {
		return mm.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "add"})
	}
	return mm.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "info " + row.path})
}

// projectsMenuKey handles non-Enter keys in the projects menu.
//   - 'a' adds the current directory (same as the + row)
//   - 'd' unregisters the highlighted project (memory preserved)
// Returns handled=false for keys it does not consume so the
// generic menu keypath can still process navigation/filtering.
func (m Model) projectsMenuKey(key string) (tea.Model, tea.Cmd, bool) {
	if m.menu.kind != menuProjects {
		return m, nil, false
	}
	switch key {
	case "a", "A":
		next, _ := m.closeMenu()
		mm := next.(Model)
		model, cmd := mm.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "add"})
		return model, cmd, true
	case "d", "D":
		rows := m.projectRows()
		if len(rows) == 0 {
			return m, nil, false
		}
		row := rows[minInt(m.menu.cursor, len(rows)-1)]
		if row.isAdd {
			return m, nil, false
		}
		next, _ := m.closeMenu()
		mm := next.(Model)
		model, cmd := mm.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "remove " + row.path})
		return model, cmd, true
	}
	return m, nil, false
}

// renderProjectsMenu draws the projects list inside a bordered
// panel: registered projects with name + key + memory size, the
// current project highlighted with "(current)", and the "+ add"
// action row in accent.
func (m Model) renderProjectsMenu() string {
	rows := m.projectRows()
	nReal := len(rows) - 1 // minus the add row

	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("Projects") + "\n")
	switch {
	case nReal == 0:
		b.WriteString(m.palette.Dim.Render("no projects registered — add one to start its memory") + "\n")
	default:
		b.WriteString(m.palette.Dim.Render(fmt.Sprintf("%d project(s) registered · per-project memory under %%USERPROFILE%%\\supercli-data\\projects\\", nReal)) + "\n")
	}
	b.WriteString("\n")

	for i, r := range rows {
		selected := i == m.menu.cursor
		cursor := "  "
		if selected {
			cursor = m.palette.HeaderMode.Render("❯ ")
		}

		var line string
		if r.isAdd {
			label := "+  add current directory"
			if selected {
				line = m.palette.HeaderMode.Render(label)
			} else {
				line = m.palette.Marker.Render(label)
			}
		} else {
			name := filepath.Base(r.path)
			// Memory size on disk (best-effort stat)
			size := "(no memory yet)"
			if r.key != "" && m.dataDir != "" {
				if fi, err := os.Stat(filepath.Join(m.dataDir, "projects", r.key, "memory.db")); err == nil {
					size = fmt.Sprintf("%.1f KB", float64(fi.Size())/1024)
				}
			}
			// Compose: ✓ basename (current)?  size  · key
			parts := []string{m.palette.Success.Render("✓")}
			if r.isCwd {
				parts = append(parts, m.palette.Bold.Render(name), m.palette.Dim.Render("(current)"))
			} else if selected {
				parts = append(parts, m.palette.HeaderMode.Render(name))
			} else {
				parts = append(parts, m.palette.Bold.Render(name))
			}
			parts = append(parts, m.palette.Dim.Render("  "+size+" · "+r.key))
			line = strings.Join(parts, " ")
		}
		b.WriteString(cursor + line + "\n")
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(0, 2).
		Render(b.String())

	hint := m.palette.InputHint.Render("↑↓ select · Enter info · a add · d remove · ESC back")
	return panel + "\n" + hint
}
