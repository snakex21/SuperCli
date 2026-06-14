package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/codexauth"
)

// menu_accounts.go is the graphical front for the multi-account
// ChatGPT (Codex) auth that the /login, /logout and /accounts
// commands implement. The menu does NOT duplicate auth logic — it
// lists accounts via codexauth.ListAccounts and triggers the
// existing slash handlers, so there is one source of truth.

// accountRow is one line in the accounts menu: either a logged-in
// account or the trailing "add account" action.
type accountRow struct {
	label  string // account label; "" for the add-action row
	isAdd  bool
}

// accountRows builds the menu rows: every logged-in account first
// (sorted, default first by ListAccounts order), then an
// "add account" action row.
func (m Model) accountRows() []accountRow {
	var rows []accountRow
	for _, label := range m.loggedInAccounts() {
		rows = append(rows, accountRow{label: label})
	}
	rows = append(rows, accountRow{isAdd: true})
	return rows
}

// loggedInAccounts returns the labels of accounts with usable
// tokens on disk, default first. Empty when nobody is logged in.
func (m Model) loggedInAccounts() []string {
	labels, err := codexauth.ListAccounts(m.dataDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(m.dataDir, label, codexauth.Options{})
		if mgr.LoggedIn() {
			out = append(out, label)
		}
	}
	return out
}

// accountsMenuEnter handles Enter in the accounts menu: on the
// add-action row it starts a login (default account if none exists
// yet, otherwise a prompt for a label); on an account row it does
// nothing (use 'd' to log out — see menuAccountsKey).
func (m Model) accountsMenuEnter() (tea.Model, tea.Cmd) {
	rows := m.accountRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[minInt(m.menu.cursor, len(rows)-1)]
	if !row.isAdd {
		// Selecting an existing account is a no-op; logging out is
		// the 'd' key. Keep Enter harmless so a stray keypress
		// never disturbs a live login.
		return m, nil
	}
	// Add account: first account uses the default (bare /login);
	// subsequent ones go through the labelled login form.
	next, _ := m.closeMenu()
	mm := next.(Model)
	if len(m.loggedInAccounts()) == 0 {
		return mm.dispatchSlashCommand(SlashCommand{Name: "login"})
	}
	// Reuse the provider form as a single-field label prompt.
	mm.mode = modeMenu
	mm.menu = interactiveMenu{kind: menuAccountLabel, form: []string{""}, formAt: 0}
	return mm, nil
}

// menuAccountsKey handles non-Enter keys in the accounts menu.
// 'd' logs out the account under the cursor (never the add row).
func (m Model) menuAccountsKey(key string) (tea.Model, tea.Cmd, bool) {
	if m.menu.kind != menuAccounts {
		return m, nil, false
	}
	if key != "d" && key != "D" {
		return m, nil, false
	}
	rows := m.accountRows()
	if len(rows) == 0 {
		return m, nil, false
	}
	row := rows[minInt(m.menu.cursor, len(rows)-1)]
	if row.isAdd {
		return m, nil, false
	}
	next, _ := m.closeMenu()
	mm := next.(Model)
	model, cmd := mm.dispatchSlashCommand(SlashCommand{Name: "logout", Args: row.label})
	return model, cmd, true
}

// renderAccountsMenu draws the accounts list with the add action.
func (m Model) renderAccountsMenu() string {
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("ChatGPT accounts") + "\n\n")
	rows := m.accountRows()
	for i, r := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		var line string
		if r.isAdd {
			line = "+ add account"
		} else {
			line = "✓ " + r.label
		}
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Dim.Render(line)
		}
		b.WriteString(prefix + line + "\n")
	}
	n := len(rows) - 1 // minus the add row
	if n >= 2 {
		b.WriteString("\n" + m.palette.Dim.Render(fmt.Sprintf("%d accounts — requests round-robin across them", n)))
	} else if n == 1 {
		b.WriteString("\n" + m.palette.Dim.Render("add a second account to enable round-robin"))
	}
	b.WriteString("\n\n" + m.palette.InputHint.Render("↑↓ select · Enter add · d log out · ESC back"))
	return b.String()
}

// renderAccountLabelMenu draws the single-field prompt for naming a
// new account before login.
func (m Model) renderAccountLabelMenu() string {
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("Name the new account") + "\n\n")
	label := ""
	if len(m.menu.form) > 0 {
		label = m.menu.form[0]
	}
	b.WriteString("  label: " + m.palette.HeaderMode.Render(label+"▌") + "\n")
	b.WriteString("\n" + m.palette.Dim.Render("e.g. praca, prywatne — saved as auth-<label>.json"))
	b.WriteString("\n\n" + m.palette.InputHint.Render("type · Enter sign in · ESC cancel"))
	return b.String()
}

// accountLabelKey handles typing/Enter/backspace in the label
// prompt. Returns handled=false for keys it does not consume.
func (m Model) accountLabelKey(key string) (tea.Model, tea.Cmd, bool) {
	if m.menu.kind != menuAccountLabel {
		return m, nil, false
	}
	switch key {
	case "enter":
		label := ""
		if len(m.menu.form) > 0 {
			label = strings.TrimSpace(m.menu.form[0])
		}
		if label == "" {
			return m, nil, true // ignore empty submit
		}
		next, _ := m.closeMenu()
		mm := next.(Model)
		model, cmd := mm.dispatchSlashCommand(SlashCommand{Name: "login", Args: label})
		return model, cmd, true
	case "backspace":
		if len(m.menu.form) > 0 && len(m.menu.form[0]) > 0 {
			r := []rune(m.menu.form[0])
			m.menu.form[0] = string(r[:len(r)-1])
		}
		return m, nil, true
	default:
		// Single printable character → append to the label.
		if len(key) == 1 && key[0] >= 0x20 && key[0] < 0x7f {
			if len(m.menu.form) == 0 {
				m.menu.form = []string{""}
			}
			m.menu.form[0] += key
			return m, nil, true
		}
	}
	return m, nil, false
}
