package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

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
	m.enterMenu(interactiveMenu{kind: menuProjects, cursor: 0})
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
	page := menuPage{title: m.tr("Projects", "Projekty"), subtitle: m.tr("Project memory travels with the application data.", "Pamięć projektów jest przechowywana razem z danymi aplikacji."),
		footer: m.tr("Enter use · A add · I details · D remove", "Enter użyj · A dodaj · I szczegóły · D usuń")}
	for i, row := range rows {
		label, badge := row.name, ""
		if row.isAdd {
			label = m.tr("+  add current directory", "+  dodaj bieżący folder")
		}
		if row.isActive {
			badge = m.tr("[active]", "[aktywny]")
		}
		page.items = append(page.items, menuListItem{label: label, badge: badge})
		if i == m.menu.cursor {
			page.detailTitle = label
			if row.isAdd {
				page.detail = []string{m.tr("Register the current folder as a project.", "Zarejestruj bieżący folder jako projekt."), m.home}
				continue
			}
			size := m.tr("(no memory yet)", "(jeszcze bez pamięci)")
			if row.key != "" && m.dataDir != "" {
				if fi, err := os.Stat(filepath.Join(m.dataDir, "projects", row.key, "memory.db")); err == nil {
					size = fmt.Sprintf("%.1f KB", float64(fi.Size())/1024)
				}
			}
			page.detail = []string{row.path, "", size, row.key, row.model}
			if row.isCwd {
				page.detail = append(page.detail, m.tr("(cwd)", "(bieżący folder)"))
			}
		}
	}
	return m.renderMenuPage(page)
}
