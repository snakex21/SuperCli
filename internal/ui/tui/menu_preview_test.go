package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/agent"
)

func previewTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), NoColor: true, Language: "en"})
	m.width, m.height = 80, 24
	next, cmd := m.openPreviewMenu()
	if cmd != nil {
		t.Fatal("opening the menu must not launch or read the clipboard")
	}
	return next.(Model)
}

func previewTestKey(m Model, key tea.KeyMsg) (Model, tea.Cmd) {
	next, cmd := m.Update(key)
	return next.(Model), cmd
}

func TestPreviewActionPreservesDraftAndActiveRun(t *testing.T) {
	m := New(Options{Home: t.TempDir(), NoColor: true})
	m.width, m.height = 80, 24
	m.input.SetValue("draft while worker runs")
	m.pendingAttachments = []string{"attached.png"}
	m.busy = true
	events := make(chan agent.Event)
	m.eventCh = events
	cancelled := false
	m.cancel.Arm(cancelRun, func() { cancelled = true })
	m.workerViews = []workerView{{id: "worker-1", status: "running"}}
	next, _ := m.openActionsMenu()
	m = next.(Model)
	count := 0
	for i, row := range m.filteredActionRows() {
		if row.id == "preview" {
			count++
			m.menu.cursor = i
		}
	}
	if count != 1 {
		t.Fatalf("preview action count = %d", count)
	}
	next, cmd := m.selectAction()
	m = next.(Model)
	if cmd != nil || m.menu.kind != menuPreview {
		t.Fatal("preview must open a local menu")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("localhost:3000"), Paste: true})
	next, _ = m.Update(runEventMsg{ev: agent.MessageEvent{Text: "streamed answer"}})
	m = next.(Model)
	if m.current != "streamed answer" || m.menu.kind != menuPreview {
		t.Fatal("menu interrupted stream consumption")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuActions {
		t.Fatal("Esc did not return to the action centre")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyCtrlK})
	if m.mode != modeNormal || m.quitting || cancelled || !m.busy || !m.cancel.IsArmed() || m.eventCh != events {
		t.Fatal("preview navigation changed the active run")
	}
	if m.input.Value() != "draft while worker runs" || !m.input.Focused() || !reflect.DeepEqual(m.pendingAttachments, []string{"attached.png"}) || len(m.workerViews) != 1 {
		t.Fatal("preview navigation discarded the composer or worker state")
	}
	next, _ = m.openPreviewMenu()
	if got := next.(Model).menu.previewInput.Value(); got != "localhost:3000" {
		t.Fatalf("address was not retained: %q", got)
	}
}

func TestPreviewAddressEditingAndNavigation(t *testing.T) {
	m := previewTestModel(t)
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("localhost:3000"), Paste: true})
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyHome})
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("http://")})
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if got := m.menu.previewInput.Value(); got != "http://localhost:3001" {
		t.Fatalf("edited URL = %q", got)
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.menu.cursor != 1 || m.menu.previewInput.Focused() {
		t.Fatal("down did not select built-in preview")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.menu.cursor != 2 {
		t.Fatal("Tab did not select browser")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.menu.cursor != 0 || !m.menu.previewInput.Focused() {
		t.Fatal("up did not return to URL editing")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.menu.previewInput.Value() != "" {
		t.Fatal("Ctrl+U did not clear the address")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.cursor != 1 {
		t.Fatal("Enter in the address did not select the open action")
	}
}

func TestPreviewPasteIsDeferredAndIgnoresStaleMenu(t *testing.T) {
	m := previewTestModel(t)
	reads := 0
	cmd := previewPasteCmd(m.menu.previewID, func() (string, error) { reads++; return "localhost:3000", nil })
	if reads != 0 {
		t.Fatal("clipboard read happened outside tea.Cmd")
	}
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyDown})
	next, _ := m.Update(cmd())
	m = next.(Model)
	if reads != 1 || m.menu.previewInput.Value() != "localhost:3000" || m.menu.previewInput.Focused() || m.menu.cursor != 1 {
		t.Fatal("pending paste did not reach the original address field")
	}
	oldID := m.menu.previewID
	m, _ = previewTestKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	next, _ = m.openPreviewMenu()
	m = next.(Model)
	next, _ = m.Update(previewPasteMsg{id: oldID, text: "evil.example"})
	if next.(Model).menu.previewInput.Value() != "localhost:3000" {
		t.Fatal("stale clipboard reply reached a new menu")
	}
}

func TestPreviewInvalidURLNeverReturnsLaunchCommand(t *testing.T) {
	for _, raw := range []string{"", "javascript:alert(1)", "https://user:pass@example.com", "localhost:99999", "https://example.com/%00"} {
		t.Run(raw, func(t *testing.T) {
			m := previewTestModel(t)
			m.menu.previewInput.SetValue(raw)
			m.menu.cursor = 2
			next, cmd := m.selectPreviewAction()
			m = next.(Model)
			if cmd != nil || m.menu.formErr != m.tr("preview.invalid") || m.menu.cursor != 0 || !m.menu.previewInput.Focused() {
				t.Fatal("invalid URL was not rejected before launch")
			}
		})
	}
}

func TestPreviewSelectionNormalizesAddressWithoutStarting(t *testing.T) {
	m := previewTestModel(t)
	m.menu.previewInput.SetValue("localhost:3000/path?q=1")
	m.menu.cursor = 1
	next, cmd := m.selectPreviewAction()
	m = next.(Model)
	if cmd == nil || m.previewURL != "http://localhost:3000/path?q=1" || m.menu.previewInput.Value() != m.previewURL {
		t.Fatal("preview selection did not use the shared URL normalization")
	}
	// Do not execute this production command: the launcher is tested below
	// through injected dependencies and fake processes.
}

type previewRegularFile struct{ mode os.FileMode }

func (f previewRegularFile) Name() string       { return "supercli-web" }
func (f previewRegularFile) Size() int64        { return 1 }
func (f previewRegularFile) Mode() os.FileMode  { return f.mode }
func (f previewRegularFile) ModTime() time.Time { return time.Time{} }
func (f previewRegularFile) IsDir() bool        { return f.mode.IsDir() }
func (f previewRegularFile) Sys() any           { return nil }

func TestPreviewCommandUsesOnlySiblingGUIAndSeparateArguments(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "supercli.exe")
	normalized := "http://localhost:3000/path?q=a&command=calc"
	dataDir := filepath.Join(filepath.Dir(executable), "portable data")
	expected := previewBinaryPath(executable, runtime.GOOS)
	calls := 0
	launch := previewLaunchers{
		executable: func() (string, error) { calls++; return executable, nil },
		stat: func(name string) (os.FileInfo, error) {
			if name != expected {
				t.Fatalf("GUI binary = %q, want sibling %q", name, expected)
			}
			return previewRegularFile{}, nil
		},
		start: func(cmd *exec.Cmd) error {
			want := []string{expected, "--preview", normalized, "--preview-language", "pl", "--data-dir", dataDir}
			if cmd.Path != expected || !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != filepath.Dir(expected) {
				t.Fatalf("unexpected command: %+v", cmd)
			}
			return nil
		},
		openBrowser: func(string) error { t.Fatal("built-in preview invoked system browser"); return nil },
	}
	cmd := sitePreviewCmd(7, normalized, false, "pl", dataDir, launch)
	if calls != 0 {
		t.Fatal("executable lookup occurred outside tea.Cmd")
	}
	msg := cmd().(previewOpenedMsg)
	if calls != 1 || msg.err != nil || msg.systemBrowser || msg.id != 7 || msg.url != normalized {
		t.Fatalf("result = %+v", msg)
	}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		name := "supercli-web"
		if goos == "windows" {
			name += ".exe"
		}
		if got := previewBinaryPath(executable, goos); got != filepath.Join(filepath.Dir(executable), name) {
			t.Fatalf("%s binary = %q", goos, got)
		}
	}
}

func TestPreviewUnavailableDoesNotStartAnyFallback(t *testing.T) {
	for _, fixture := range []struct {
		name string
		info os.FileInfo
		err  error
	}{
		{name: "missing", err: os.ErrNotExist},
		{name: "directory", info: previewRegularFile{mode: os.ModeDir}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			launch := previewLaunchers{
				executable:  func() (string, error) { return filepath.Join(t.TempDir(), "supercli"), nil },
				stat:        func(string) (os.FileInfo, error) { return fixture.info, fixture.err },
				start:       func(*exec.Cmd) error { t.Fatal("started unavailable GUI"); return nil },
				openBrowser: func(string) error { t.Fatal("started fallback browser"); return nil },
			}
			msg := sitePreviewCmd(1, "https://example.com", false, "en", "data", launch)().(previewOpenedMsg)
			if msg.err == nil || !msg.unavailable {
				t.Fatalf("result = %+v", msg)
			}
			m := previewTestModel(t)
			msg.id = m.menu.previewID
			next, _ := m.Update(msg)
			if got := next.(Model).menu.formErr; got != m.tr("preview.unavailable") {
				t.Fatalf("unavailable notice = %q", got)
			}
		})
	}
}

func TestPreviewBrowserAndStartFailureResults(t *testing.T) {
	normalized := "https://example.com/path?q=one&two=three"
	failure := errors.New("launch denied")
	launch := previewLaunchers{openBrowser: func(got string) error {
		if got != normalized {
			t.Fatalf("browser URL = %q", got)
		}
		return failure
	}}
	msg := sitePreviewCmd(3, normalized, true, "en", "data", launch)().(previewOpenedMsg)
	if !msg.systemBrowser || !errors.Is(msg.err, failure) {
		t.Fatalf("browser result = %+v", msg)
	}
	m := previewTestModel(t)
	msg.id = m.menu.previewID
	next, _ := m.Update(msg)
	if !strings.Contains(next.(Model).menu.formErr, "launch denied") {
		t.Fatal("launch error was not shown")
	}
	msg.err = nil
	next, cmd := m.Update(msg)
	if cmd == nil || !next.(Model).statusSuccess || next.(Model).statusOverride != m.tr("preview.opened") {
		t.Fatal("browser success was not shown")
	}
}

type fakePreviewProcess struct {
	startErr error
	waited   chan struct{}
	exit     chan struct{}
	done     chan struct{}
}

func (p *fakePreviewProcess) Start() error { return p.startErr }
func (p *fakePreviewProcess) Wait() error  { close(p.waited); <-p.exit; close(p.done); return nil }

func TestPreviewProcessWaitDoesNotBlockAndFailedStartIsNotWaited(t *testing.T) {
	p := &fakePreviewProcess{waited: make(chan struct{}), exit: make(chan struct{}), done: make(chan struct{})}
	if err := startPreviewProcess(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-p.exit:
		default:
			close(p.exit)
		}
	})
	select {
	case <-p.waited:
	case <-time.After(time.Second):
		t.Fatal("successful process was not reaped")
	}
	close(p.exit)
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not finish after process exit")
	}
	failed := &fakePreviewProcess{startErr: errors.New("start failed"), waited: make(chan struct{}), exit: make(chan struct{})}
	if !errors.Is(startPreviewProcess(failed), failed.startErr) {
		t.Fatal("start failure was lost")
	}
	select {
	case <-failed.waited:
		t.Fatal("Wait called after failed Start")
	default:
	}
}

func TestPreviewMenuFitsNarrowTerminal(t *testing.T) {
	m := previewTestModel(t)
	m.width = 38
	m.menu.previewInput.SetValue("http://localhost:3000/" + strings.Repeat("long-path/", 40))
	m.menu.previewInput.CursorEnd()
	view := m.View()
	if !strings.Contains(view, m.tr("preview.open")) || !strings.Contains(view, m.tr("preview.browser")) {
		t.Fatal("preview actions are missing")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("menu overflows terminal: %q", line)
		}
	}
}

func TestPreviewGUIStartFailurePreservesActiveState(t *testing.T) {
	failure := errors.New("cannot start GUI")
	launch := previewLaunchers{
		executable:  func() (string, error) { return filepath.Join(t.TempDir(), "supercli"), nil },
		stat:        func(string) (os.FileInfo, error) { return previewRegularFile{}, nil },
		start:       func(*exec.Cmd) error { return failure },
		openBrowser: func(string) error { t.Fatal("GUI failure launched a fallback"); return nil },
	}
	m := previewTestModel(t)
	m.busy = true
	m.input.SetValue("unsent draft")
	events := make(chan agent.Event)
	m.eventCh = events
	msg := sitePreviewCmd(m.menu.previewID, "https://example.com", false, "en", m.dataDir, launch)().(previewOpenedMsg)
	if !errors.Is(msg.err, failure) || msg.unavailable {
		t.Fatalf("GUI Start error = %+v", msg)
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	if !strings.Contains(m.menu.formErr, failure.Error()) || !m.busy || m.eventCh != events || m.input.Value() != "unsent draft" {
		t.Fatal("GUI launch failure changed agent or composer state")
	}
}
