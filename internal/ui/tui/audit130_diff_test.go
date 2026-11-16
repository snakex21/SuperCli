package tui

// audit-130: /diff empty state, a small change, and a very large change.
// /diff reads the session's fileops.Tracker (not git), so git repos are only
// used to show that uncommitted git changes are not what /diff reports.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/tools/fileops"
)

func audit130DiffModel(t *testing.T, home string, tracker *fileops.Tracker) Model {
	t.Helper()
	if home == "" {
		home = t.TempDir()
	}
	m := New(Options{Home: home, DataDir: t.TempDir(), Language: "pl", Tracker: tracker})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return out.(Model)
}

// runSlash types the command like a user and feeds every resulting message
// back into the model (single level; /diff returns one slashResultMsg).
func audit130RunSlash(t *testing.T, m Model, text string) Model {
	t.Helper()
	m = audit130Type(m, text)
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	m = audit130Drain(t, m, cmd, 0)
	return m
}

func audit130Drain(t *testing.T, m Model, cmd tea.Cmd, depth int) Model {
	t.Helper()
	if cmd == nil || depth > 3 {
		return m
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return m // a ticker or waiter; not part of /diff
	}
	switch v := msg.(type) {
	case tea.BatchMsg:
		for _, c := range v {
			m = audit130Drain(t, m, c, depth+1)
		}
		return m
	case slashResultMsg:
		out, next := m.Update(v)
		return audit130Drain(t, out.(Model), next, depth+1)
	}
	return m
}

func audit130Transcript(m Model) string {
	m.refreshTranscript()
	return ansi.Strip(m.viewport.View()) + "\n" + ansi.Strip(m.chat.renderWithSpinner(m.palette, ""))
}

func TestAudit130DiffEmptyStates(t *testing.T) {
	empty := textFor("pl", "tui.slash.807564bac8")
	cases := map[string]*fileops.Tracker{"nil-tracker": nil, "empty-tracker": fileops.NewTracker(200)}
	for name, tr := range cases {
		m := audit130RunSlash(t, audit130DiffModel(t, "", tr), "/diff")
		if text := audit130Transcript(m); !strings.Contains(text, empty) {
			t.Fatalf("%s: empty-state %q not shown:\n%s", name, empty, text)
		}
		if m.busy {
			t.Fatalf("%s: /diff left the TUI busy", name)
		}
	}
}

func audit130Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=audit", "GIT_AUTHOR_EMAIL=audit@example.invalid", "GIT_COMMITTER_NAME=audit", "GIT_COMMITTER_EMAIL=audit@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// /diff is the session tracker, not `git diff`. Clean and dirty repos both
// show the empty state when SuperCli itself changed nothing.
func TestAudit130DiffGitReposAreNotTheSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	empty := textFor("pl", "tui.slash.807564bac8")
	for _, dirty := range []bool{false, true} {
		repo := t.TempDir()
		audit130Git(t, repo, "init", "-q")
		if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		audit130Git(t, repo, "add", "a.txt")
		audit130Git(t, repo, "commit", "-q", "-m", "init")
		if dirty {
			if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("two\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		m := audit130RunSlash(t, audit130DiffModel(t, repo, fileops.NewTracker(200)), "/diff")
		text := audit130Transcript(m)
		if !strings.Contains(text, empty) {
			t.Fatalf("dirty=%v: %s", dirty, text)
		}
		if dirty {
			t.Logf("UX note: repo has an uncommitted change (a.txt one->two) but /diff says %q", empty)
		}
	}
}

func TestAudit130DiffSmallChange(t *testing.T) {
	tr := fileops.NewTracker(200)
	tr.Record("src/main.go", "edit", "package main\nfunc old() {}\n", "package main\nfunc renamed() {}\n")
	m := audit130RunSlash(t, audit130DiffModel(t, "", tr), "/diff")
	text := audit130Transcript(m)
	for _, want := range []string{"src/main.go", "func old()", "func renamed()"} {
		if !strings.Contains(text, want) {
			t.Fatalf("diff lacks %q:\n%s", want, text)
		}
	}
}

func TestAudit130DiffLargeChangeIsBoundedAndFast(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "line %05d of a large generated file\n", i)
	}
	tr := fileops.NewTracker(200)
	tr.Record("big.txt", "write", "", b.String())
	m := audit130DiffModel(t, "", tr)
	start := time.Now()
	m = audit130RunSlash(t, m, "/diff")
	view := m.View()
	elapsed := time.Since(start)
	t.Logf("/diff 5000-line change: dispatch+render %v, chat rows=%d", elapsed, strings.Count(audit130Transcript(m), "\n"))
	if elapsed > 3*time.Second {
		t.Fatalf("/diff on a 5000-line change took %v", elapsed)
	}
	if n := len(strings.Split(view, "\n")); n > 30 {
		t.Fatalf("view %d rows > 30", n)
	}
	text := audit130Transcript(m)
	if strings.Contains(text, "line 04999") || strings.Count(text, "line 0") > 100 {
		t.Fatalf("large diff not bounded (%d content lines rendered)", strings.Count(text, "line 0"))
	}
	// Scrolling works: PgUp moves away from the bottom.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if out.(Model).viewport.AtBottom() {
		t.Fatalf("PgUp did not scroll the /diff output")
	}
}

// The truncation marker must say how many lines were hidden.
func TestAudit130DiffTruncationCountIsAccurate(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "l%d\n", i)
	}
	tr := fileops.NewTracker(200)
	tr.Record("big.txt", "write", "", strings.TrimSuffix(b.String(), "\n"))
	text := audit130Transcript(audit130RunSlash(t, audit130DiffModel(t, "", tr), "/diff"))
	// The model is Polish, so the marker is the localized one.
	if want := fmt.Sprintf(textFor("pl", "tui.diff.more_lines"), 4980); !strings.Contains(text, want) {
		t.Fatalf("hidden-line marker %q (5000 lines, 20 shown) missing:\n%s", want, text)
	}
}

// In a Polish UI the /diff summary header should not be English.
func TestAudit130DiffHeaderLocalized(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "l%d\n", i)
	}
	tr := fileops.NewTracker(200)
	tr.Record("a.txt", "edit", "x", "y")
	tr.Record("b.txt", "write", "", b.String()) // long enough to be truncated
	text := audit130Transcript(audit130RunSlash(t, audit130DiffModel(t, "", tr), "/diff"))
	for _, english := range []string{"file change(s) in session", "more lines"} {
		if strings.Contains(text, english) {
			t.Fatalf("English chrome %q in pl /diff:\n%s", english, text)
		}
	}
	if want := fmt.Sprintf(textFor("pl", "tui.diff.summary"), 2); !strings.Contains(text, want) {
		t.Fatalf("localized summary %q missing:\n%s", want, text)
	}
}

// The /diff summary used to be written as Markdown italics ("_[...]_") but
// shown as plain text, so the user saw literal underscores and a double space.
func TestAudit130DiffHeaderHasNoRawMarkup(t *testing.T) {
	tr := fileops.NewTracker(200)
	tr.Record("a.txt", "edit", "x", "y")
	m := audit130RunSlash(t, audit130DiffModel(t, "", tr), "/diff")
	view := ansi.Strip(m.View())
	for _, raw := range []string{"_[", "]_", "|  ["} {
		if strings.Contains(view, raw) {
			t.Fatalf("raw markup %q visible in /diff:\n%s", raw, view)
		}
	}
}
