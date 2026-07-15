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

// menu_projects.go is the interactive front-end for the named-workspace
// store owned by internal/storage/memory/workspace.go and the /projects
// slash command. The menu never duplicates storage logic — it reads the
// workspace and triggers the existing slash handler, so there is one
// source of truth.
//
// Open with:  /projects  (no args)  ·  Ctrl+P  ·  the 'p' shortcut
//
// Keys:
//   ↑↓     navigate
//   Enter  make the highlighted project active (or "add" on the + row)
//   i      info on the highlighted project
//   a      add the current working directory
//   d      remove the highlighted project (memory stays on disk)
//   ESC    back

// projectRow is one line in the projects menu: either a registered
// project (with its key + memory size) or the trailing "+ add"
// action row.
type projectRow struct {
	path     string // absolute path; "" for the add-action row
	name     string // display name (workspace name, falls back to basename)
	key      string // ProjectKey(path)
	model    string // preferred model, if any
	isCwd    bool   // true if this row matches the cwd
	isActive bool   // true if this is the active project
	isAdd    bool   // true for the trailing "+ add" row
}

// projectRows builds the menu rows: every registered project first
// (sorted by path), then an "+ add current directory" action row.
// Projects come from the named-workspace store, merged with any legacy
// path→key entries so nothing a user registered before disappears.
func (m Model) projectRows() []projectRow {
	if m.dataDir == "" {
		// No data dir → no project memory at all. Still show the
		// add row so the user sees something instead of an empty
		// panel.
		return []projectRow{{isAdd: true}}
	}
	ws := memory.LoadWorkspace(m.dataDir)
	byPath := map[string]memory.Project{}
	for _, p := range ws.Projects {
		byPath[p.Path] = p
	}
	// Merge legacy map entries not yet promoted into the workspace.
	for path := range memory.LoadProjectsMap(m.dataDir) {
		if _, ok := byPath[path]; !ok {
			byPath[path] = memory.Project{Name: filepath.Base(path), Path: path}
		}
	}
	cwd, _ := os.Getwd()

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	rows := make([]projectRow, 0, len(paths)+1)
	for _, p := range paths {
		proj := byPath[p]
		name := proj.Name
		if name == "" {
			name = filepath.Base(p)
		}
		rows = append(rows, projectRow{
			path:     p,
			name:     name,
			key:      memory.ProjectKey(p),
			model:    proj.Model,
			isCwd:    p == cwd,
			isActive: p == ws.Active,
		})
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
//   - on a project row: make that project active (/projects use)
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
	return mm.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "use " + row.path})
}

// projectsMenuKey handles non-Enter keys in the projects menu.
//   - 'i' shows info for the highlighted project
//   - 'a' adds the current directory (same as the + row)
//   - 'd' unregisters the highlighted project (memory preserved)
//
// Returns handled=false for keys it does not consume so the
// generic menu keypath can still process navigation/filtering.
func (m Model) projectsMenuKey(key string) (tea.Model, tea.Cmd, bool) {
	if m.menu.kind != menuProjects {
		return m, nil, false
	}
	switch key {
	case "i", "I":
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
		model, cmd := mm.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "info " + row.path})
		return model, cmd, true
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
	width := maxInt(24, m.menuWidth()-6)

	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Projects", "Projekty")) + "\n")
	switch {
	case nReal == 0:
		b.WriteString(m.palette.Dim.Render(truncateText(m.tr("no projects registered — add one to start its memory", "brak projektów — dodaj projekt, aby uruchomić jego pamięć"), width)) + "\n")
	default:
		desc := m.tr(fmt.Sprintf("%d project(s) registered · per-project memory under %%USERPROFILE%%\\supercli-data\\projects\\", nReal), fmt.Sprintf("Zarejestrowane projekty: %d · pamięć w %%USERPROFILE%%\\supercli-data\\projects\\", nReal))
		b.WriteString(m.palette.Dim.Render(truncateText(desc, width)) + "\n")
	}
	b.WriteString("\n")

	start, end := 0, len(rows)
	if m.height > 0 {
		start, end = menuWindow(len(rows), m.menu.cursor, m.height-7)
	}
	for i := start; i < end; i++ {
		r := rows[i]
		selected := i == m.menu.cursor
		cursor := "  "
		if selected {
			cursor = m.palette.HeaderMode.Render("> ")
		}

		var line string
		if r.isAdd {
			label := m.tr("+  add current directory", "+  dodaj bieżący folder")
			if selected {
				line = m.palette.HeaderMode.Render(label)
			} else {
				line = m.palette.Marker.Render(label)
			}
		} else {
			name := r.name
			if name == "" {
				name = filepath.Base(r.path)
			}
			// Memory size on disk (best-effort stat)
			size := m.tr("(no memory yet)", "(jeszcze bez pamięci)")
			if r.key != "" && m.dataDir != "" {
				if fi, err := os.Stat(filepath.Join(m.dataDir, "projects", r.key, "memory.db")); err == nil {
					size = fmt.Sprintf("%.1f KB", float64(fi.Size())/1024)
				}
			}
			// ASCII markers stay readable in legacy Windows terminal fonts.
			marker := "[on]"
			if r.isActive {
				marker = m.tr("[active]", "[aktywny]")
			}
			parts := []string{marker}
			nameParts := []string{name}
			if r.isActive {
				nameParts = append(nameParts, m.tr("(active)", "(aktywny)"))
			}
			if r.isCwd {
				nameParts = append(nameParts, m.tr("(cwd)", "(bieżący folder)"))
			}
			parts = append(parts, nameParts...)
			meta := "  " + size + " · " + r.key
			if r.model != "" {
				meta += " · " + r.model
			}
			parts = append(parts, meta)
			line = truncateText(strings.Join(parts, " "), width-2)
			if selected {
				line = m.palette.HeaderMode.Render(line)
			} else {
				line = m.palette.Bold.Render(line)
			}
		}
		b.WriteString(cursor + line + "\n")
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(0, 2).
		Render(b.String())

	hint := m.palette.InputHint.Render(truncateText(m.tr("↑↓ select · Enter use · i info · a add · d remove · Esc back", "↑↓ wybierz · Enter użyj · i informacje · a dodaj · d usuń · Esc wróć"), m.menuWidth()))
	return panel + "\n" + hint
}
