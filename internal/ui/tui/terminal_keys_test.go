package tui

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type onboardInputFixture struct{ onboardModel }

func (m onboardInputFixture) Init() tea.Cmd { return nil }
func (m onboardInputFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.onboardModel.Update(msg)
	return onboardInputFixture{next.(onboardModel)}, cmd
}

type keyChunkReader struct{ chunks []string }

func (r *keyChunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

func TestFirstRunProviderArrowsAcrossInputReads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
	}{
		{"whole CSI", []string{"\x1b[B\r"}},
		{"whole SS3", []string{"\x1bOB\r"}},
		{"split after escape", []string{"\x1b", "[B\r"}},
		{"split CSI", []string{"\x1b[", "B\r"}},
		{"split SS3", []string{"\x1bO", "B\r"}},
		{"one byte per read", []string{"\x1b", "[", "B", "\r"}},
		{"Tab fallback", []string{"\t\r"}},
		{"Shift Tab fallback", []string{"\t\x1b[Z\t\r"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			model := onboardInputFixture{onboardModel{step: onboardMenu, language: "en", choices: []onboardChoice{{label: "first", kind: "echo"}, {label: "second", kind: "echo"}}}}
			program := NewProgram(model, tea.WithContext(ctx), tea.WithInput(&keyChunkReader{chunks: tc.chunks}), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
			final, err := program.Run()
			if err != nil {
				t.Fatal(err)
			}
			got := final.(onboardInputFixture).onboardModel
			if got.aborted || got.step != onboardDone || got.cursor != 1 {
				t.Fatalf("arrow was treated as Escape/search text: aborted=%v step=%v cursor=%d filter=%q", got.aborted, got.step, got.cursor, got.filter)
			}
		})
	}
}

type terminalInputFixture struct{ keys []tea.KeyMsg }

func (m terminalInputFixture) Init() tea.Cmd { return nil }
func (m terminalInputFixture) View() string  { return "" }
func (m terminalInputFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keys, ok := msg.(terminalKeyBatchMsg); ok {
		return updateTerminalKeyBatch(m, keys)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if key.Type == tea.KeyEnter && !key.Alt {
			return m, tea.Quit
		}
		m.keys = append(m.keys, key)
	}
	return m, nil
}

func readTerminalKeys(t *testing.T, chunks []string) []tea.KeyMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	program := NewProgram(terminalInputFixture{}, tea.WithContext(ctx), tea.WithInput(&keyChunkReader{chunks: chunks}), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	final, err := program.Run()
	if err != nil {
		t.Fatal(err)
	}
	return final.(terminalInputFixture).keys
}

func TestTerminalNavigationPreservesTextAtEverySplit(t *testing.T) {
	// Exercise the real input decoder, not just synthesized KeyMsg values.
	for _, tc := range []struct{ sequence, key string }{
		{"\x1b[A", "up"}, {"\x1b[B", "down"}, {"\x1b[C", "right"}, {"\x1b[D", "left"},
		{"\x1bOA", "up"}, {"\x1bOB", "down"}, {"\x1bOC", "right"}, {"\x1bOD", "left"},
		{"\x1b[H", "home"}, {"\x1b[F", "end"}, {"\x1b[5~", "pgup"}, {"\x1b[6~", "pgdown"},
		{"\x1b[1;2A", "shift+up"}, {"\x1b[1;3B", "alt+down"}, {"\x1b[1;5C", "ctrl+right"}, {"\x1b[1;6D", "ctrl+shift+left"},
		{"\x1b[1;8H", "alt+ctrl+shift+home"}, {"\x1b[Z", "shift+tab"},
	} {
		for split := 1; split < len(tc.sequence); split++ {
			t.Run(fmt.Sprintf("%s/split-%d", tc.key, split), func(t *testing.T) {
				got := readTerminalKeys(t, []string{tc.sequence[:split], tc.sequence[split:] + "suffix\r"})
				if len(got) != 2 || got[0].String() != tc.key || string(got[1].Runes) != "suffix" || got[1].Alt || got[1].Paste {
					t.Fatalf("lost navigation/text order: %#v", got)
				}
			})
		}
	}
}

func TestTerminalKeysPreservePasteAltAndCancel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   []tea.KeyMsg
	}{
		{"ordinary search", []string{"LM Studio\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("LM")}, {Type: tea.KeySpace, Runes: []rune(" ")}, {Type: tea.KeyRunes, Runes: []rune("Studio")}}},
		{"Alt Enter", []string{"\x1b\r\r"}, []tea.KeyMsg{{Type: tea.KeyEnter, Alt: true}}},
		{"Alt text", []string{"\x1bx\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Alt: true, Runes: []rune("x")}}},
		{"unknown split sequence", []string{"\x1b[", "9~\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Alt: true, Runes: []rune("[")}, {Type: tea.KeyRunes, Runes: []rune("9~")}}},
		{"paste containing escape", []string{"\x1b[200~\x1b[B\x1b[201~\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Paste: true, Runes: []rune("\x1b[B")}}},
		{"cancel after incomplete key", []string{"\x1b[", "\x03\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Alt: true, Runes: []rune("[")}, {Type: tea.KeyCtrlC}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readTerminalKeys(t, tc.chunks); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestStandaloneEscapeStillSkipsFirstRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	model := onboardInputFixture{onboardModel{step: onboardMenu}}
	program := NewProgram(model, tea.WithContext(ctx), tea.WithInput(&keyChunkReader{chunks: []string{"\x1b"}}), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	final, err := program.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !final.(onboardInputFixture).aborted {
		t.Fatal("Escape no longer skips setup")
	}
}

func TestIncompleteTerminalPrefixExpiresWithoutEatingNextKey(t *testing.T) {
	delivered := make(chan tea.Msg, 4)
	filter := terminalKeyFilter{send: func(msg tea.Msg) { delivered <- msg }}
	if got := filter.filter(nil, tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("[")}); got != nil {
		t.Fatal("prefix should wait for continuation")
	}
	select {
	case timeout := <-delivered:
		got := filter.filter(nil, timeout)
		if key, ok := got.(tea.KeyMsg); !ok || key.String() != "alt+[" {
			t.Fatalf("prefix lost on timeout: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("incomplete prefix never expired")
	}
	next := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("B")}
	if got := filter.filter(nil, next); !reflect.DeepEqual(got, next) {
		t.Fatalf("next search key was eaten: %#v", got)
	}
}

func TestRecoveredArrowBatchWorksInMainProviderMenu(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("unsent draft")
	m.enterMenu(interactiveMenu{kind: menuProviderPredefined})
	// An arrow and text from a single read must be processed before Enter.
	next, _ := m.Update(terminalKeyBatchMsg{{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune("LM Studio")}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.menu.kind != menuProviderForm || m.menu.form[2] != "http://localhost:1234/v1" || m.input.Value() != "unsent draft" {
		t.Fatalf("provider navigation lost selection or draft: %+v", m.menu)
	}
}

func TestTerminalRecoveryKeepsUnicodeAndIgnoresStaleTimeout(t *testing.T) {
	filter := terminalKeyFilter{send: func(tea.Msg) {}}
	defer filter.flush()
	filter.filter(nil, tea.KeyMsg{Type: tea.KeyEsc})
	oldTimeout := terminalEscapeTimeout{filter.generation}
	got := filter.filter(nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[Bżółć")})
	want := terminalKeyBatchMsg{{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune("żółć")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unicode remainder changed: %#v", got)
	}
	filter.filter(nil, tea.KeyMsg{Type: tea.KeyEsc})
	if got := filter.filter(nil, oldTimeout); got != nil {
		t.Fatalf("old timer flushed a later Escape: %#v", got)
	}
	if got := filter.filter(nil, terminalEscapeTimeout{filter.generation}); !reflect.DeepEqual(got, tea.KeyMsg{Type: tea.KeyEsc}) {
		t.Fatalf("current Escape lost: %#v", got)
	}
}
