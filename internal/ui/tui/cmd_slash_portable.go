package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/storage/session"
)

// portableExportArgs reports whether /export arguments request the
// portable session format ("session" or "json", optionally followed by
// a path) and returns the remaining path argument.
func portableExportArgs(args string) (string, bool) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", false
	}
	switch strings.ToLower(fields[0]) {
	case "session", "json":
		rest := strings.TrimSpace(strings.TrimSpace(args)[len(fields[0]):])
		return unquotePath(rest), true
	}
	return "", false
}

// unquotePath strips one pair of matching surrounding quotes.
func unquotePath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 {
		if (p[0] == '"' && p[len(p)-1] == '"') || (p[0] == '\'' && p[len(p)-1] == '\'') {
			p = strings.TrimSpace(p[1 : len(p)-1])
		}
	}
	return p
}

// resolveAgainst returns p unchanged when absolute, otherwise joined to base.
func resolveAgainst(base, p string) string {
	if filepath.IsAbs(p) || base == "" {
		return p
	}
	return filepath.Join(base, p)
}

// exportPortableSession writes the active session as a portable
// .supercli-session.json file. dir is the export directory used by the
// Markdown export (data directory or home).
func (m Model) exportPortableSession(store *session.Store, sessionID, dir, pathArg string) tea.Msg {
	dm := m.marker
	sess, err := store.Get(sessionID)
	if err != nil {
		return slashResultMsg{Err: err}
	}
	path := filepath.Join(dir, session.PortableFilename(sess))
	if pathArg != "" {
		path = resolveAgainst(dir, pathArg)
	}
	f, err := os.Create(path)
	if err != nil {
		return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.portable_export_failed"), err)}
	}
	doc, err := store.WritePortable(context.Background(), sess.ID, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.portable_export_failed"), err)}
	}
	return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf(m.tr("tui.slash.portable_exported"), len(doc.Messages), path))}
}

// handleSlashImport imports a portable session file as a new session
// recorded for the TUI working directory.
func (m Model) handleSlashImport(cmd SlashCommand) (tea.Model, tea.Cmd) {
	store := m.sessionStore
	dm := m.marker
	cwd := m.home
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	pathArg := unquotePath(cmd.Args)
	return m, func() tea.Msg {
		if pathArg == "" {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.import_usage"))}
		}
		if store == nil {
			return slashResultMsg{Body: dm.Diff(m.tr("tui.slash.11acbac2d2"))}
		}
		path := resolveAgainst(cwd, pathArg)
		f, err := os.Open(path)
		if err != nil {
			return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.import_failed"), err)}
		}
		defer f.Close()
		sess, err := store.ImportPortableReader(context.Background(), f, session.PortableImportOptions{Cwd: cwd})
		switch {
		case errors.Is(err, session.ErrPortableTooLarge):
			return slashResultMsg{Err: errors.New(fmt.Sprintf(m.tr("tui.slash.import_too_large"), session.MaxPortableBytes>>20))}
		case errors.Is(err, session.ErrPortableInvalid):
			return slashResultMsg{Err: fmt.Errorf("%s (%w)", fmt.Sprintf(m.tr("tui.slash.import_invalid"), path), err)}
		case err != nil:
			return slashResultMsg{Err: fmt.Errorf(m.tr("tui.slash.import_failed"), err)}
		}
		return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf(m.tr("tui.slash.imported"), sess.MessageCount, sess.Title, sess.ID, sess.ID))}
	}
}
