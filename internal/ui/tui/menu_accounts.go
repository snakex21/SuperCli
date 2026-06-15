package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/account/codexauth"
)

// menu_accounts.go is the graphical front for the multi-account
// ChatGPT (Codex) auth that the /login, /logout and /accounts
// commands implement. The menu does NOT duplicate auth logic — it
// lists accounts via codexauth.ListAccounts and triggers the
// existing slash handlers, so there is one source of truth.

// accountRow is one line in the accounts menu: either a logged-in
// account or the trailing "add account" action.
type accountRow struct {
	label string // account label; "" for the add-action row
	email string // account email (from the token), "" if unknown
	plan  string // ChatGPT plan type, "" if unknown
	isAdd bool
}

// accountRows builds the menu rows: every logged-in account first
// (sorted, default first by ListAccounts order), then an
// "add account" action row.
func (m Model) accountRows() []accountRow {
	var rows []accountRow
	for _, label := range m.loggedInAccounts() {
		r := accountRow{label: label}
		// Best-effort: decode the account's email/plan from its
		// token so the user sees WHICH ChatGPT account this is, not
		// just the local label. Never hits the network.
		mgr := codexauth.NewManagerFor(m.dataDir, label, codexauth.Options{})
		if info, err := mgr.Account(); err == nil && info.LoggedIn {
			r.email = info.Email
			r.plan = info.PlanType
		}
		rows = append(rows, r)
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

// renderAccountsMenu draws the accounts list inside a bordered
// panel: live accounts with a green check, the add-action row in
// accent, and a round-robin status footer.
func (m Model) renderAccountsMenu() string {
	rows := m.accountRows()
	nAccts := len(rows) - 1 // minus the add row

	var body strings.Builder
	title := m.palette.PanelTitle.Render("ChatGPT accounts")
	body.WriteString(title + "\n")
	body.WriteString(m.palette.Dim.Render("sign in to one or more ChatGPT accounts") + "\n\n")

	for i, r := range rows {
		selected := i == m.menu.cursor
		cursor := "  "
		if selected {
			cursor = m.palette.HeaderMode.Render("❯ ")
		}

		var line string
		if r.isAdd {
			label := "+  add account"
			if selected {
				line = m.palette.HeaderMode.Render(label)
			} else {
				line = m.palette.Marker.Render(label)
			}
		} else {
			check := m.palette.Success.Render("✓")
			name := r.label
			if selected {
				name = m.palette.HeaderMode.Render(name)
			} else {
				name = m.palette.Bold.Render(name)
			}
			// Show WHICH ChatGPT account this label maps to:
			// email (and plan) decoded from the token. Falls back
			// gracefully when the token has no email claim.
			detail := ""
			if r.email != "" {
				detail = r.email
			}
			if r.plan != "" {
				if detail != "" {
					detail += " · "
				}
				detail += r.plan
			}
			if detail != "" {
				detail = "  " + m.palette.Dim.Render("("+detail+")")
			}
			line = fmt.Sprintf("%s  %s%s", check, name, detail)
		}
		body.WriteString(cursor + line + "\n")
	}

	// Round-robin status footer.
	body.WriteString("\n")
	switch {
	case nAccts >= 2:
		body.WriteString(m.palette.Success.Render(fmt.Sprintf("● %d accounts — requests round-robin across them", nAccts)))
	case nAccts == 1:
		body.WriteString(m.palette.Dim.Render("add a second account to spread load (round-robin)"))
	default:
		body.WriteString(m.palette.Dim.Render("no accounts yet — add one to sign in"))
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(0, 2).
		Render(body.String())

	hint := m.palette.InputHint.Render("↑↓ select · Enter add · d log out · ESC back")
	return panel + "\n" + hint
}

// renderAccountLabelMenu draws the single-field prompt for naming a
// new account before login, in a bordered panel.
func (m Model) renderAccountLabelMenu() string {
	label := ""
	if len(m.menu.form) > 0 {
		label = m.menu.form[0]
	}
	var body strings.Builder
	body.WriteString(m.palette.PanelTitle.Render("Name the new account") + "\n\n")
	field := m.palette.HeaderMode.Render(" " + label + "▌ ")
	body.WriteString("label  " + field + "\n\n")
	body.WriteString(m.palette.Dim.Render("e.g. praca, prywatne — saved as auth-<label>.json"))

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(0, 2).
		Render(body.String())

	hint := m.palette.InputHint.Render("type · Enter sign in · ESC cancel")
	return panel + "\n" + hint
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
