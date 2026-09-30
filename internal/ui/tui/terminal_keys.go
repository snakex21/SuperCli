package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// NewProgram keeps fragmented terminal navigation sequences together. Bubble Tea
// treats short input reads as event boundaries, which is not guaranteed by a TTY
// or SSH connection. Complete keys and ordinary text still pass through directly.
func NewProgram(model tea.Model, opts ...tea.ProgramOption) *tea.Program {
	var program *tea.Program
	filter := terminalKeyFilter{send: func(msg tea.Msg) { program.Send(msg) }}
	program = tea.NewProgram(model, append(opts, tea.WithFilter(filter.filter))...)
	return program
}

const terminalEscapeWait = 50 * time.Millisecond

type terminalKeyBatchMsg []tea.KeyMsg
type terminalEscapeTimeout struct{ generation uint64 }

// Updates run synchronously in input order, including any text after an arrow
// in the same read. Sending the remainder asynchronously could let Enter overtake it.
func updateTerminalKeyBatch(model tea.Model, keys terminalKeyBatchMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, key := range keys {
		next, cmd := model.Update(key)
		model = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return model, nil
	}
	return model, tea.Sequence(cmds...)
}

type terminalKeyFilter struct {
	pending    string
	originals  []tea.KeyMsg
	generation uint64
	timer      *time.Timer
	send       func(tea.Msg)
}

func (f *terminalKeyFilter) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	if timeout, ok := msg.(terminalEscapeTimeout); ok {
		if timeout.generation != f.generation || f.pending == "" {
			return nil
		}
		return terminalKeysMsg(f.flush())
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return msg
	}
	var before []tea.KeyMsg
	if f.pending != "" {
		if key.Type == tea.KeyRunes && !key.Alt && !key.Paste {
			candidate := f.pending + string(key.Runes)
			for sequence, navigation := range terminalNavigationSequences {
				if strings.HasPrefix(candidate, sequence) {
					f.flush()
					keys := []tea.KeyMsg{navigation}
					if rest := candidate[len(sequence):]; rest != "" {
						keys = append(keys, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(rest)})
					}
					return terminalKeysMsg(keys)
				}
			}
			for sequence := range terminalNavigationSequences {
				if strings.HasPrefix(sequence, candidate) {
					f.pending = candidate
					f.originals = append(f.originals, key)
					f.arm()
					return nil
				}
			}
		}
		before = f.flush()
	}
	if !key.Paste && !key.Alt && key.Type == tea.KeyEsc {
		f.pending = "\x1b"
	} else if !key.Paste && key.Alt && key.Type == tea.KeyRunes && (string(key.Runes) == "[" || string(key.Runes) == "O") {
		f.pending = "\x1b" + string(key.Runes)
	}
	if f.pending != "" {
		f.originals = []tea.KeyMsg{key}
		f.arm()
	} else {
		before = append(before, key)
	}
	return terminalKeysMsg(before)
}

func (f *terminalKeyFilter) arm() {
	if f.timer != nil {
		f.timer.Stop()
	}
	f.generation++
	generation := f.generation
	f.timer = time.AfterFunc(terminalEscapeWait, func() { f.send(terminalEscapeTimeout{generation}) })
}

func (f *terminalKeyFilter) flush() []tea.KeyMsg {
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.generation++
	keys := f.originals
	f.pending, f.originals = "", nil
	return keys
}

func terminalKeysMsg(keys []tea.KeyMsg) tea.Msg {
	switch len(keys) {
	case 0:
		return nil
	case 1:
		return keys[0]
	default:
		return terminalKeyBatchMsg(keys)
	}
}

// Only recognized navigation is reconstructed. Unknown sequences, Alt text and
// bracketed paste are preserved, rather than guessed to be keyboard shortcuts.
var terminalNavigationSequences = func() map[string]tea.KeyMsg {
	keys := map[string]tea.KeyMsg{
		"\x1b[Z":  {Type: tea.KeyShiftTab},
		"\x1b[1~": {Type: tea.KeyHome}, "\x1b[4~": {Type: tea.KeyEnd},
		"\x1b[7~": {Type: tea.KeyHome}, "\x1b[8~": {Type: tea.KeyEnd},
		"\x1b[5~": {Type: tea.KeyPgUp}, "\x1b[6~": {Type: tea.KeyPgDown},
	}
	for _, key := range []struct {
		code                          byte
		plain, shift, ctrl, ctrlShift tea.KeyType
	}{
		{'A', tea.KeyUp, tea.KeyShiftUp, tea.KeyCtrlUp, tea.KeyCtrlShiftUp},
		{'B', tea.KeyDown, tea.KeyShiftDown, tea.KeyCtrlDown, tea.KeyCtrlShiftDown},
		{'C', tea.KeyRight, tea.KeyShiftRight, tea.KeyCtrlRight, tea.KeyCtrlShiftRight},
		{'D', tea.KeyLeft, tea.KeyShiftLeft, tea.KeyCtrlLeft, tea.KeyCtrlShiftLeft},
		{'H', tea.KeyHome, tea.KeyShiftHome, tea.KeyCtrlHome, tea.KeyCtrlShiftHome},
		{'F', tea.KeyEnd, tea.KeyShiftEnd, tea.KeyCtrlEnd, tea.KeyCtrlShiftEnd},
	} {
		keys["\x1b["+string(key.code)] = tea.KeyMsg{Type: key.plain}
		if key.code >= 'A' && key.code <= 'D' {
			keys["\x1bO"+string(key.code)] = tea.KeyMsg{Type: key.plain}
		}
		types := []tea.KeyType{key.plain, key.shift, key.plain, key.shift, key.ctrl, key.ctrlShift, key.ctrl, key.ctrlShift}
		for modifier := 2; modifier <= 8; modifier++ {
			keys[fmt.Sprintf("\x1b[1;%d%c", modifier, key.code)] = tea.KeyMsg{Type: types[modifier-1], Alt: modifier == 3 || modifier == 4 || modifier == 7 || modifier == 8}
		}
	}
	return keys
}()
