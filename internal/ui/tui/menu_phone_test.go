package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/webgui/qrcode"
)

const phoneTestURL = "https://sample.trycloudflare.com/pair#0123456789abcdef"

func TestPhoneQRHalfBlocksRoundTrip(t *testing.T) {
	for _, text := range []string{"x", phoneTestURL, phoneTestURL + strings.Repeat("abcdef", 20), strings.Repeat("x", 213)} {
		code, err := qrcode.Encode(text)
		if err != nil {
			t.Fatal(err)
		}
		rendered := renderPhoneQR(code)
		rows := strings.Split(ansi.Strip(rendered), "\n")
		size := code.Size + 8
		if len(rows) != (size+1)/2 {
			t.Fatalf("wrong row count: %d", len(rows))
		}
		if strings.Count(rendered, "\x1b[0;38;2;0;0;0;48;2;255;255;255m") != len(rows) {
			t.Fatal("QR does not force black on white on every row")
		}
		for row, line := range rows {
			runes := []rune(line)
			if len(runes) != size {
				t.Fatalf("quiet zone cropped: %d != %d", len(runes), size)
			}
			for x, cell := range runes {
				for half := 0; half < 2; half++ {
					y := row*2 + half
					got := cell == '█' || (half == 0 && cell == '▀') || (half == 1 && cell == '▄')
					want := x >= 4 && y >= 4 && x < size-4 && y < size-4 && code.Dark(x-4, y-4)
					if got != want {
						t.Fatalf("module (%d,%d): got %v, want %v", x, y, got, want)
					}
				}
			}
		}
	}
}

func TestPhoneProtocolParser(t *testing.T) {
	for _, tc := range []struct {
		line, kind  string
		done, total int64
		text        string
	}{
		{"SUPERCLI_PHONE_NEED_DOWNLOAD 55000000\r", "SUPERCLI_PHONE_NEED_DOWNLOAD", 0, 55000000, "55000000"},
		{"SUPERCLI_PHONE_PROGRESS 120 240", "SUPERCLI_PHONE_PROGRESS", 120, 240, "120 240"},
		{"SUPERCLI_PHONE_PROGRESS 0 0", "SUPERCLI_PHONE_PROGRESS", 0, 0, "0 0"},
		{"SUPERCLI_PHONE_URL " + phoneTestURL, "SUPERCLI_PHONE_URL", 0, 0, phoneTestURL},
		{"SUPERCLI_PHONE_CLOSED expired after 24 h", "SUPERCLI_PHONE_CLOSED", 0, 0, "expired after 24 h"},
	} {
		e, ok := parsePhoneLine(tc.line)
		if !ok || e.kind != tc.kind || e.done != tc.done || e.total != tc.total || e.text != tc.text {
			t.Errorf("%q: %+v, %v", tc.line, e, ok)
		}
	}
	for _, line := range []string{"", "log message", "SUPERCLI_PHONE_URL http://host/pair", "SUPERCLI_PHONE_URL https://host/\x1b[31m", "SUPERCLI_PHONE_URL https://host/ with spaces", "SUPERCLI_PHONE_URL https://user@host/pair", "SUPERCLI_PHONE_URL https:///pair", "SUPERCLI_PHONE_NEED_DOWNLOAD -1", "SUPERCLI_PHONE_NEED_DOWNLOAD 1 2", "SUPERCLI_PHONE_NEED_DOWNLOAD 99999999999999999999999", "SUPERCLI_PHONE_PROGRESS 1", "SUPERCLI_PHONE_PROGRESS -1 10", "SUPERCLI_PHONE_PROGRESS 11 10", "SUPERCLI_PHONE_PROGRESS 1 -1", "SUPERCLI_PHONE_PROGRESS 1 two"} {
		if e, ok := parsePhoneLine(line); ok {
			t.Errorf("accepted malformed line %q: %+v", line, e)
		}
	}
	if e, _ := parsePhoneLine("SUPERCLI_PHONE_CLOSED done\x1b\x00"); e.text != "done" {
		t.Fatal("terminal controls leaked")
	}
}

type fakePhoneProcess struct {
	reader   *io.PipeReader
	writer   *io.PipeWriter
	input    *fakePhoneInput
	commands chan string
	finished chan struct{}
	once     sync.Once
	code     int
	stubborn bool
	killed   atomic.Bool
}

type fakePhoneInput struct{ process *fakePhoneProcess }

func (w *fakePhoneInput) Write(data []byte) (int, error) {
	p := w.process
	command := string(data)
	p.commands <- command
	if command == "pair\n" {
		_, _ = io.WriteString(p.writer, "SUPERCLI_PHONE_URL "+phoneTestURL+"-new\n")
	}
	if command == "stop\n" && !p.stubborn {
		p.finish()
	}
	return len(data), nil
}
func (*fakePhoneInput) Close() error { return nil }
func newFakePhoneProcess(code int, stubborn bool) *fakePhoneProcess {
	r, w := io.Pipe()
	p := &fakePhoneProcess{reader: r, writer: w, commands: make(chan string, 16), finished: make(chan struct{}), code: code, stubborn: stubborn}
	p.input = &fakePhoneInput{p}
	return p
}
func (p *fakePhoneProcess) finish()               { p.once.Do(func() { _ = p.writer.Close(); close(p.finished) }) }
func (p *fakePhoneProcess) Stdin() io.WriteCloser { return p.input }
func (p *fakePhoneProcess) Stdout() io.ReadCloser { return p.reader }
func (p *fakePhoneProcess) Wait() (int, error) {
	<-p.finished
	if p.code != 0 {
		return p.code, errors.New("fake helper exit")
	}
	return 0, nil
}
func (p *fakePhoneProcess) Kill() error { p.killed.Store(true); p.finish(); return nil }
func (p *fakePhoneProcess) lines(text string, finish bool) {
	go func() {
		_, _ = io.WriteString(p.writer, text)
		if finish {
			p.finish()
		}
	}()
}

func phoneTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: "pl", NoColor: true})
	m.width, m.height = 110, 48
	return m
}
func fakePhoneLaunch(t *testing.T, start func(*exec.Cmd) (phoneProcess, error)) phoneLaunchers {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "supercli")
	return phoneLaunchers{executable: func() (string, error) { return executable, nil }, stat: func(path string) (os.FileInfo, error) {
		if path != previewBinaryPath(executable, runtime.GOOS) {
			t.Errorf("wrong helper path: %s", path)
		}
		return previewRegularFile{}, nil
	}, start: start}
}
func awaitPhoneCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("phone command blocked")
		return nil
	}
}
func awaitPhoneDone(t *testing.T, s *phoneSession) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("phone session did not stop")
	}
}
func phoneUpdate(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}
func phoneKey(text string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)} }
func awaitPhoneInput(t *testing.T, p *fakePhoneProcess, want string) {
	t.Helper()
	select {
	case got := <-p.commands:
		if got != want {
			t.Fatalf("stdin = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("missing stdin %q", want)
	}
}

func TestPhoneFakeProcessConsentProgressPairDisconnect(t *testing.T) {
	m := phoneTestModel(t)
	first, second := newFakePhoneProcess(3, false), newFakePhoneProcess(0, false)
	t.Cleanup(first.finish)
	t.Cleanup(second.finish)
	starts := 0
	launch := fakePhoneLaunch(t, func(cmd *exec.Cmd) (phoneProcess, error) {
		starts++
		want := []string{cmd.Path, "--phone", "--home", m.home, "--workspace", m.home, "--data-dir", m.dataDir}
		if starts == 2 {
			want = append(want, "--phone-allow-download")
		}
		if !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != filepath.Dir(cmd.Path) {
			t.Errorf("wrong helper launch: %+v", cmd)
		}
		if starts == 1 {
			first.lines("SUPERCLI_PHONE_NEED_DOWNLOAD 55000000\n", true)
			return first, nil
		}
		second.lines("SUPERCLI_PHONE_PROGRESS 128 256\nSUPERCLI_PHONE_URL "+phoneTestURL+"\n", false)
		return second, nil
	})
	m.phone.launch = &launch
	next, cmd := m.dispatchSlashCommand(SlashCommand{Name: "telefon"})
	m = next.(Model)
	if starts != 0 || m.menu.kind != menuPhone {
		t.Fatal("launcher ran inside Update")
	}
	m, cmd = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	if !m.phone.consent || cmd != nil || !strings.Contains(m.View(), "[t/N]") {
		t.Fatal("missing explicit download consent")
	}
	// Closing the prompt cannot grant consent, and reopening cannot restart it.
	m, _ = phoneUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	next, cmd = m.dispatchSlashCommand(SlashCommand{Name: "phone"})
	m = next.(Model)
	if cmd != nil || starts != 1 {
		t.Fatal("reopening consent relaunched the helper")
	}
	m, cmd = phoneUpdate(t, m, phoneKey("t"))
	m, cmd = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	if m.phone.downloaded != 128 || !strings.Contains(m.View(), "128 / 256 B") || !strings.Contains(m.View(), "░") {
		t.Fatal("progress not rendered")
	}
	m, cmd = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	if m.phone.url != phoneTestURL || m.phone.code == nil || !strings.Contains(m.View(), "\x1b[0;38;2;0;0;0;48;2;255;255;255m") {
		t.Fatal("pairing QR missing")
	}
	s := m.phone.session
	m, _ = phoneUpdate(t, m, phoneKey("n"))
	awaitPhoneInput(t, second, "pair\n")
	m, cmd = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	if m.phone.url != phoneTestURL+"-new" {
		t.Fatal("new URL did not replace previous pairing")
	}
	m, _ = phoneUpdate(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.phone.session != s || s.ctx.Err() != nil || m.mode == modeMenu {
		t.Fatal("Esc stopped the tunnel")
	}
	next, reopen := m.openPhoneMenu()
	m = next.(Model)
	if reopen != nil || m.phone.session != s {
		t.Fatal("reopening started another tunnel")
	}
	m, _ = phoneUpdate(t, m, phoneKey("r"))
	awaitPhoneInput(t, second, "stop\n")
	awaitPhoneDone(t, s)
	if m.phone.session != nil || m.phone.url != "" || m.phone.code != nil || second.killed.Load() {
		t.Fatal("graceful disconnect failed")
	}
	m, _ = phoneUpdate(t, m, phoneEventMsg{session: s, event: phoneEvent{kind: "SUPERCLI_PHONE_URL", text: phoneTestURL}})
	if m.phone.url != "" {
		t.Fatal("stale event restored revoked QR")
	}
}

func TestPhoneNeedDownloadRequiresExitThree(t *testing.T) {
	for _, code := range []int{0, 1, 3} {
		t.Run(string(rune('0'+code)), func(t *testing.T) {
			m := phoneTestModel(t)
			p := newFakePhoneProcess(code, false)
			t.Cleanup(p.finish)
			launch := fakePhoneLaunch(t, func(*exec.Cmd) (phoneProcess, error) {
				p.lines("SUPERCLI_PHONE_NEED_DOWNLOAD 55\n", true)
				return p, nil
			})
			m.phone.launch = &launch
			next, cmd := m.openPhoneMenu()
			m = next.(Model)
			m, _ = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
			if m.phone.consent != (code == 3) {
				t.Fatal("wrong consent state for helper exit")
			}
		})
	}
}

func TestPhoneConsentDefaultsToNo(t *testing.T) {
	for _, language := range []string{"pl", "en"} {
		for _, key := range []tea.KeyMsg{phoneKey("N"), {Type: tea.KeyEnter}} {
			m := phoneTestModel(t)
			m.language = language
			m.enterMenu(interactiveMenu{kind: menuPhone})
			m.phone.consent = true
			m.phone.status = "phone.download"
			m, cmd := phoneUpdate(t, m, key)
			if cmd != nil || m.phone.consent || m.phone.session != nil {
				t.Fatal("default/negative answer started download")
			}
		}
	}
}

func TestPhoneQuitKillsOnlyOwnedUnresponsiveHelper(t *testing.T) {
	m := phoneTestModel(t)
	p := newFakePhoneProcess(0, true)
	t.Cleanup(p.finish)
	launch := fakePhoneLaunch(t, func(*exec.Cmd) (phoneProcess, error) {
		p.lines("SUPERCLI_PHONE_URL "+phoneTestURL+"\n", false)
		return p, nil
	})
	m.phone.launch = &launch
	next, cmd := m.openPhoneMenu()
	m = next.(Model)
	m, _ = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	s := m.phone.session
	m, cmd = phoneUpdate(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Fatal("Ctrl+C did not quit")
	}
	if _, ok := awaitPhoneCmd(t, cmd).(tea.QuitMsg); !ok {
		t.Fatal("quit was not delivered after helper cleanup")
	}
	awaitPhoneInput(t, p, "stop\n")
	awaitPhoneDone(t, s)
	if !p.killed.Load() {
		t.Fatal("unresponsive helper was not killed after grace period")
	}
}

func TestPhoneClosedAndLaunchFailure(t *testing.T) {
	m := phoneTestModel(t)
	p := newFakePhoneProcess(0, false)
	t.Cleanup(p.finish)
	launch := fakePhoneLaunch(t, func(*exec.Cmd) (phoneProcess, error) {
		p.lines("SUPERCLI_PHONE_URL "+phoneTestURL+"\nSUPERCLI_PHONE_CLOSED expired after 24 h\n", false)
		return p, nil
	})
	m.phone.launch = &launch
	next, cmd := m.openPhoneMenu()
	m = next.(Model)
	m, cmd = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	s := m.phone.session
	m, _ = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	awaitPhoneDone(t, s)
	if m.phone.session != nil || m.phone.url != "" || !strings.Contains(m.View(), "expired after 24 h") {
		t.Fatal("automatic closure not shown")
	}
	failure := errors.New("fake launch denied")
	launch.start = func(*exec.Cmd) (phoneProcess, error) { return nil, failure }
	next, cmd = m.startPhone(false)
	m = next.(Model)
	m, _ = phoneUpdate(t, m, awaitPhoneCmd(t, cmd))
	if m.phone.session != nil || !strings.Contains(m.View(), failure.Error()) {
		t.Fatal("launch error not shown")
	}
}

func TestPhoneSmallTerminalKeepsWholeLink(t *testing.T) {
	for _, dimensions := range [][2]int{{110, 48}, {110, 10}, {35, 25}, {16, 8}} {
		m := phoneTestModel(t)
		m.width, m.height = dimensions[0], dimensions[1]
		m.enterMenu(interactiveMenu{kind: menuPhone})
		m.phone.url = phoneTestURL
		m.phone.code, _ = qrcode.Encode(phoneTestURL)
		m.phone.status = "phone.ready"
		view := m.View()
		plain := ansi.Strip(view)
		if !strings.Contains(strings.ReplaceAll(plain, "\n", ""), phoneTestURL) {
			t.Fatalf("link truncated at %v", dimensions)
		}
		for _, line := range strings.Split(plain, "\n") {
			if ansi.StringWidth(line) > m.width {
				t.Fatalf("line overflows at %v: %q", dimensions, line)
			}
		}
		tooSmall := dimensions[0] < m.phone.code.Size+8 || dimensions[1] < 20
		if strings.Contains(view, "\x1b[0;38;2;0;0;0;48;2;255;255;255m") == tooSmall {
			t.Fatalf("wrong QR visibility at %v", dimensions)
		}
		if tooSmall && !strings.Contains(strings.ReplaceAll(plain, "\n", ""), m.tr("phone.small")) {
			t.Fatal("missing resize explanation")
		}
	}
}

func TestPhoneMenuHelpAndAutocomplete(t *testing.T) {
	for _, language := range []string{"pl", "en"} {
		m := phoneTestModel(t)
		m.language = language
		for _, name := range []string{"telefon", "phone"} {
			if !strings.Contains(HelpContentFor(language), "/"+name) {
				t.Fatalf("help omits %s", name)
			}
			found := false
			for _, item := range buildSlashItems(nil, language) {
				if item.Label == "/"+name {
					found = true
					if strings.HasPrefix(item.Desc, "tui.") {
						t.Fatal("untranslated slash description")
					}
				}
			}
			if !found {
				t.Fatalf("autocomplete omits %s", name)
			}
		}
		rows := m.actionRows()
		found := false
		for i, row := range rows {
			if row.id == "phone" {
				found = true
				if i == 0 || rows[i-1].id != "preview" {
					t.Fatal("phone action not beside preview")
				}
			}
		}
		if !found {
			t.Fatal("phone action missing")
		}
	}
}
