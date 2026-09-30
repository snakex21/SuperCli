package tui

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
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

type terminalInputFixture struct {
	keys   []tea.KeyMsg
	inputs []tea.Msg
}

func (m terminalInputFixture) Init() tea.Cmd { return nil }
func (m terminalInputFixture) View() string  { return "" }
func (m terminalInputFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if inputs, ok := msg.(terminalInputBatchMsg); ok {
		return updateTerminalInputBatch(m, inputs)
	}
	if mouse, ok := msg.(tea.MouseMsg); ok {
		m.inputs = append(m.inputs, mouse)
	}
	if keys, ok := msg.(terminalKeyBatchMsg); ok {
		return updateTerminalKeyBatch(m, keys)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if key.Type == tea.KeyEnter && !key.Alt {
			return m, tea.Quit
		}
		m.keys = append(m.keys, key)
		m.inputs = append(m.inputs, key)
	}
	return m, nil
}

func runTerminalInputFixture(t *testing.T, chunks []string) terminalInputFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	program := NewProgram(terminalInputFixture{}, tea.WithContext(ctx), tea.WithInput(&keyChunkReader{chunks: chunks}), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	final, err := program.Run()
	if err != nil {
		t.Fatal(err)
	}
	return final.(terminalInputFixture)
}

func readTerminalKeys(t *testing.T, chunks []string) []tea.KeyMsg {
	t.Helper()
	return runTerminalInputFixture(t, chunks).keys
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

func TestTerminalMousePreservesEventsAndTextAtSupportedSplits(t *testing.T) {
	sequences := []string{
		"\x1b[<64;10;5M", "\x1b[<65;10;5M", "\x1b[<66;10;5M", "\x1b[<67;10;5M",
		"\x1b[<0;10;5M", "\x1b[<1;10;5M", "\x1b[<2;10;5M", "\x1b[<0;10;5m",
		"\x1b[<35;10;5M", "\x1b[<32;10;5m", "\x1b[<28;10;5M", "\x1b[<128;10;5M",
		"\x1b[Ma*%", "\x1b[M *%", "\x1b[M#*%", "\x1b[MC*%", "\x1b[M<*%",
	}
	for _, sequence := range sequences {
		whole := runTerminalInputFixture(t, []string{sequence + "żółć\r"}).inputs
		if len(whole) != 2 {
			t.Fatalf("whole sequence did not decode as mouse and text: %q %#v", sequence, whole)
		}
		if _, ok := whole[0].(tea.MouseMsg); !ok {
			t.Fatalf("whole sequence has no mouse: %q %#v", sequence, whole)
		}
		for split := 1; split < len(sequence); split++ {
			// At these X10 splits upstream emits an opaque CSI prefix, rather
			// than key events. The limitation has its own regression below.
			if strings.HasPrefix(sequence, "\x1b[M") && split >= 3 {
				continue
			}
			t.Run(fmt.Sprintf("%q/split-%d", sequence, split), func(t *testing.T) {
				got := runTerminalInputFixture(t, []string{sequence[:split], sequence[split:] + "żółć\r"}).inputs
				if !reflect.DeepEqual(got, whole) {
					t.Fatalf("fragmented mouse/text changed: got %#v want %#v", got, whole)
				}
			})
		}
		chunks := make([]string, 0, len(sequence)+1)
		for i := 0; i < len(sequence); i++ {
			chunks = append(chunks, sequence[i:i+1])
		}
		chunks = append(chunks, "żółć\r")
		if got := runTerminalInputFixture(t, chunks).inputs; !reflect.DeepEqual(got, whole) {
			t.Fatalf("one-byte mouse reads changed: %q got %#v want %#v", sequence, got, whole)
		}
	}
}

func TestFirstRunProviderMouseAcrossInputReads(t *testing.T) {
	for _, sequence := range []string{"\x1b[<65;10;5M", "\x1b[Ma*%"} {
		for split := 1; split < len(sequence); split++ {
			// At these X10 splits upstream emits an opaque CSI prefix, rather
			// than key events. The limitation has its own regression below.
			if strings.HasPrefix(sequence, "\x1b[M") && split >= 3 {
				continue
			}
			t.Run(fmt.Sprintf("%q/split-%d", sequence, split), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				model := onboardInputFixture{onboardModel{step: onboardMenu, language: "en", choices: []onboardChoice{{label: "first", kind: "echo"}, {label: "second", kind: "echo"}}}}
				program := NewProgram(model, tea.WithContext(ctx), tea.WithInput(&keyChunkReader{chunks: []string{sequence[:split], sequence[split:] + "\r"}}), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
				final, err := program.Run()
				if err != nil {
					t.Fatal(err)
				}
				got := final.(onboardInputFixture).onboardModel
				if got.aborted || got.step != onboardDone || got.cursor != 1 || got.filter != "" {
					t.Fatalf("mouse became Escape/search text: aborted=%v step=%v cursor=%d filter=%q", got.aborted, got.step, got.cursor, got.filter)
				}
			})
		}
	}
}

func TestTerminalMousePasteAltAndIncompleteFragments(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   []tea.KeyMsg
	}{
		{"mouse inside paste", []string{"\x1b[200~\x1b[<65;10;5M\x1b[Ma*%\x1b[201~\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Paste: true, Runes: []rune("\x1b[<65;10;5M\x1b[Ma*%")}}},
		{"Alt less than", []string{"\x1b<\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Alt: true, Runes: []rune("<")}}},
		{"invalid SGR", []string{"\x1b[", "<65;bad\r"}, []tea.KeyMsg{{Type: tea.KeyRunes, Alt: true, Runes: []rune("[")}, {Type: tea.KeyRunes, Runes: []rune("<65;bad")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readTerminalKeys(t, tc.chunks); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
	delivered := make(chan tea.Msg, 4)
	filter := terminalKeyFilter{send: func(msg tea.Msg) { delivered <- msg }}
	defer filter.flush()
	prefix := tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("[")}
	body := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<65;10;")}
	filter.filter(nil, prefix)
	filter.filter(nil, body)
	select {
	case timeout := <-delivered:
		if got := filter.filter(nil, timeout); !reflect.DeepEqual(got, terminalKeyBatchMsg{prefix, body}) {
			t.Fatalf("incomplete mouse fragment lost on timeout: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("incomplete mouse report never expired")
	}
	next := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")}
	if got := filter.filter(nil, next); !reflect.DeepEqual(got, next) {
		t.Fatalf("text after timeout changed: %#v", got)
	}
}

func TestRecoveredMouseBatchWorksInMainProviderMenu(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("unsent draft")
	m.enterMenu(interactiveMenu{kind: menuProviderPredefined})
	next, _ := m.Update(terminalInputBatchMsg{tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("LM Studio")}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.menu.kind != menuProviderForm || m.menu.form[2] != "http://localhost:1234/v1" || m.input.Value() != "unsent draft" {
		t.Fatalf("mouse/text order lost provider selection or draft: %+v", m.menu)
	}
}

func TestProviderDetectionBetweenMouseFragments(t *testing.T) {
	m := onboardModel{step: onboardDetect, language: "en", width: 80, height: 24}
	filter := terminalKeyFilter{send: func(tea.Msg) {}}
	defer filter.flush()
	filter.filter(nil, tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("[")})
	filter.filter(nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<65;")})
	detected := onboardDetectedMsg{}
	if got := filter.filter(m, detected); !reflect.DeepEqual(got, detected) {
		t.Fatalf("detection completion was delayed: %#v", got)
	}
	next, _ := m.Update(detected)
	m = next.(onboardModel)
	if m.step != onboardMenu || len(m.choices) == 0 || m.View() == (onboardModel{step: onboardDetect, language: "en", width: 80, height: 24}).View() {
		t.Fatal("detection did not replace finding-local-server view with provider menu")
	}
	got := filter.filter(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("10;5M")})
	mouse, ok := got.(tea.MouseMsg)
	if !ok {
		t.Fatalf("mouse not recovered across detection: %#v", got)
	}
	next, _ = m.Update(mouse)
	m = next.(onboardModel)
	if m.cursor != 1 || m.filter != "" {
		t.Fatalf("mouse corrupted detected provider menu: cursor=%d filter=%q", m.cursor, m.filter)
	}
}

func TestTerminalMouseAcrossDecoderBufferBoundary(t *testing.T) {
	for _, padding := range []int{244, 250, 251, 252, 253, 254, 255, 256, 257} {
		t.Run(fmt.Sprintf("prefix-%d", padding), func(t *testing.T) {
			prefix := strings.Repeat("x", padding)
			inputs := runTerminalInputFixture(t, []string{prefix + "\x1b[<65;10;5Msuffix\r"}).inputs
			var before, after strings.Builder
			seen := false
			for _, msg := range inputs {
				if mouse, ok := msg.(tea.MouseMsg); ok {
					if seen || mouse.Button != tea.MouseButtonWheelDown || mouse.X != 9 || mouse.Y != 4 {
						t.Fatalf("unexpected mouse across buffer: %#v", inputs)
					}
					seen = true
					continue
				}
				key, ok := msg.(tea.KeyMsg)
				if !ok || key.Type != tea.KeyRunes {
					t.Fatalf("unexpected input across buffer: %#v", inputs)
				}
				if seen {
					after.WriteString(string(key.Runes))
				} else {
					before.WriteString(string(key.Runes))
				}
			}
			if !seen || before.String() != prefix || after.String() != "suffix" {
				t.Fatalf("input order across buffer changed: %#v", inputs)
			}
		})
	}
}

func TestTerminalX10OpaquePrefixIsNotGuessed(t *testing.T) {
	// Bubble Tea 1.3.10 exposes ESC[M from a short read as a private CSI event
	// backed by its reused buffer. Recovering that opaque prefix would require
	// joining raw input before Bubble Tea, including TTY setup and cancellation.
	// Preserve the following text instead of guessing any unknown CSI as a mouse.
	got := runTerminalInputFixture(t, []string{"\x1b[M", "a*%hello\r"}).inputs
	want := []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a*%hello")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("opaque X10 prefix was guessed or following text lost: %#v", got)
	}
}

func TestInvalidMouseContinuationDoesNotBlockFollowingInput(t *testing.T) {
	filter := terminalKeyFilter{send: func(tea.Msg) {}}
	defer filter.flush()
	filter.filter(nil, tea.KeyMsg{Type: tea.KeyEsc})
	first := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;")}
	filter.filter(nil, first)
	timeout := terminalEscapeTimeout{filter.generation}
	next := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")}
	got := filter.filter(nil, next)
	want := terminalKeyBatchMsg{{Type: tea.KeyEsc}, first, next}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid continuation blocked/lost input: %#v", got)
	}
	if got := filter.filter(nil, timeout); got != nil {
		t.Fatalf("old mouse timeout duplicated input: %#v", got)
	}
}

func TestTerminalMouseGrammarRejectsOversizedFields(t *testing.T) {
	for _, candidate := range []string{
		"\x1b[<65;99999999999", "\x1b[<65;2147483648;5M", "\x1b[<65;;5M", "\x1b[<65;10;5;M",
	} {
		if msg, _, pending := recoverTerminalSequence(candidate); msg != nil || pending {
			t.Fatalf("invalid/oversized mouse report was buffered or recovered: %q %#v pending=%v", candidate, msg, pending)
		}
	}
}
