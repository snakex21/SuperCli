package tui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// NewProgram keeps fragmented terminal navigation and mouse sequences together.
// Bubble Tea treats short input reads as event boundaries, which is not guaranteed
// by a TTY or SSH connection. Complete events and ordinary text pass through.
func NewProgram(model tea.Model, opts ...tea.ProgramOption) *tea.Program {
	var program *tea.Program
	filter := terminalKeyFilter{send: func(msg tea.Msg) { program.Send(msg) }}
	program = tea.NewProgram(model, append(opts, tea.WithFilter(filter.filter))...)
	return program
}

const terminalEscapeWait = 50 * time.Millisecond

type terminalKeyBatchMsg []tea.KeyMsg
type terminalInputBatchMsg []tea.Msg
type terminalEscapeTimeout struct{ generation uint64 }

// Terminal replies are protocol traffic, not user search/input text.
type terminalCursorReportMsg struct{ row, column uint32 }

// Updates run synchronously in input order. Sending text after a recovered key
// or mouse event asynchronously could let Enter overtake it.
func updateTerminalInputBatch(model tea.Model, msgs terminalInputBatchMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, msg := range msgs {
		next, cmd := model.Update(msg)
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

func updateTerminalKeyBatch(model tea.Model, keys terminalKeyBatchMsg) (tea.Model, tea.Cmd) {
	msgs := make(terminalInputBatchMsg, len(keys))
	for i, key := range keys {
		msgs[i] = key
	}
	return updateTerminalInputBatch(model, msgs)
}

type terminalKeyFilter struct {
	pending    string
	originals  []tea.Msg
	generation uint64
	timer      *time.Timer
	send       func(tea.Msg)
}

func (f *terminalKeyFilter) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	if timeout, ok := msg.(terminalEscapeTimeout); ok {
		if timeout.generation != f.generation || f.pending == "" {
			return nil
		}
		return terminalMessagesMsg(f.flush())
	}
	var before []tea.Msg
	if f.pending != "" {
		if fragment, ok := terminalInputFragment(msg, strings.HasPrefix(f.pending, "\x1b[M")); ok {
			candidate := f.pending + fragment
			recovered, consumed, incomplete := recoverTerminalSequence(candidate)
			if recovered != nil {
				f.flush()
				var msgs []tea.Msg
				if _, cursorReport := recovered.(terminalCursorReportMsg); !cursorReport {
					msgs = append(msgs, recovered)
				}
				if rest := candidate[consumed:]; rest != "" {
					msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(rest)})
				}
				return terminalMessagesMsg(msgs)
			}
			if incomplete {
				f.pending = candidate
				f.originals = append(f.originals, msg)
				f.arm()
				return nil
			}
		} else if _, ok := msg.(tea.KeyMsg); !ok {
			// Commands (including provider detection) may complete between input reads.
			// They must not discard the pending terminal fragment.
			if _, mouse := msg.(tea.MouseMsg); !mouse {
				return msg
			}
		}
		before = f.flush()
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if !key.Paste && !key.Alt && key.Type == tea.KeyEsc {
			f.pending = "\x1b"
		} else if !key.Paste && key.Alt && key.Type == tea.KeyRunes && (string(key.Runes) == "[" || string(key.Runes) == "O") {
			f.pending = "\x1b" + string(key.Runes)
		}
	}
	if f.pending != "" {
		f.originals = []tea.Msg{msg}
		f.arm()
	} else {
		before = append(before, msg)
	}
	return terminalMessagesMsg(before)
}

func terminalInputFragment(msg tea.Msg, x10 bool) (string, bool) {
	if key, ok := msg.(tea.KeyMsg); ok && !key.Alt && !key.Paste {
		if key.Type == tea.KeyRunes {
			return string(key.Runes), true
		}
		if x10 && key.Type == tea.KeySpace {
			return " ", true
		}
		if x10 && (key.Type >= 0 && key.Type <= 31 || key.Type == 127) {
			return string([]byte{byte(key.Type)}), true
		}
	}
	if x10 {
		// X10 coordinates are raw bytes, including non-UTF-8 values. Bubble Tea
		// exposes those bytes as a private message; restrict this bridge to that
		// exact type and only while an X10 payload is pending.
		v := reflect.ValueOf(msg)
		if v.IsValid() && v.Type().PkgPath() == "github.com/charmbracelet/bubbletea" && v.Type().Name() == "unknownInputByteMsg" && v.Kind() == reflect.Uint8 {
			return string([]byte{byte(v.Uint())}), true
		}
	}
	return "", false
}

func recoverTerminalSequence(candidate string) (tea.Msg, int, bool) {
	for sequence, navigation := range terminalNavigationSequences {
		if strings.HasPrefix(candidate, sequence) {
			return navigation, len(sequence), false
		}
	}
	if report, consumed, incomplete := recoverTerminalCursorReport(candidate); report != nil || incomplete {
		return report, consumed, incomplete
	}
	// X10 is recoverable only while its prefix remains in key events. A read
	// ending exactly after ESC[M becomes an upstream unknownCSISequenceMsg
	// backed by a reused input buffer, so do not inspect or guess that event.
	if strings.HasPrefix(candidate, "\x1b[M") {
		if len(candidate) < 6 {
			return nil, 0, true
		}
		m := terminalMouseButton(int(candidate[3]) - 32)
		m.X, m.Y = int(candidate[4])-33, int(candidate[5])-33
		return m, 6, false
	}
	if strings.HasPrefix(candidate, "\x1b[<") {
		// Only a bounded, complete SGR grammar is reconstructed. Invalid or stale
		// fragments are replayed as their original events, including Alt and Escape.
		var values [3]int
		field, start := 0, 3
		for i := start; i < len(candidate); i++ {
			c := candidate[i]
			if c >= '0' && c <= '9' {
				if i-start >= 10 {
					return nil, 0, false
				}
				continue
			}
			if i == start {
				return nil, 0, false
			}
			n, err := strconv.ParseUint(candidate[start:i], 10, 31)
			if err != nil {
				return nil, 0, false
			}
			values[field] = int(n)
			if c == ';' && field < 2 {
				field++
				start = i + 1
				continue
			}
			if (c == 'M' || c == 'm') && field == 2 {
				m := terminalMouseButton(values[0])
				if c == 'm' && m.Action != tea.MouseActionMotion && !tea.MouseEvent(m).IsWheel() {
					m.Action, m.Type = tea.MouseActionRelease, tea.MouseRelease
				}
				m.X, m.Y = values[1]-1, values[2]-1
				return m, i + 1, false
			}
			return nil, 0, false
		}
		return nil, 0, true
	}
	for sequence := range terminalNavigationSequences {
		if strings.HasPrefix(sequence, candidate) {
			return nil, 0, true
		}
	}
	return nil, 0, false
}

// Recognize CPR only after a real Escape/CSI prefix. A user typing [29;1R or
// bracketed paste containing it must remain ordinary text. Known keys take
// precedence because modified navigation/function keys can resemble a report.
func recoverTerminalCursorReport(candidate string) (tea.Msg, int, bool) {
	if !strings.HasPrefix(candidate, "\x1b[") {
		return nil, 0, false
	}
	var values [2]uint32
	field, start := 0, 2
	for i := start; i < len(candidate); i++ {
		c := candidate[i]
		if c >= '0' && c <= '9' {
			if i-start >= 10 {
				return nil, 0, false
			}
			continue
		}
		if i == start {
			return nil, 0, false
		}
		n, err := strconv.ParseUint(candidate[start:i], 10, 32)
		if err != nil || n == 0 {
			return nil, 0, false
		}
		values[field] = uint32(n)
		if c == ';' && field == 0 {
			field++
			start = i + 1
			continue
		}
		if c == 'R' && field == 1 {
			return terminalCursorReportMsg{row: values[0], column: values[1]}, i + 1, false
		}
		return nil, 0, false
	}
	return nil, 0, true
}

// Match Bubble Tea's complete-event decoder, including its legacy Type field.
func terminalMouseButton(code int) tea.MouseMsg {
	m := tea.MouseMsg{Shift: code&4 != 0, Alt: code&8 != 0, Ctrl: code&16 != 0}
	switch {
	case code&128 != 0:
		m.Button = tea.MouseButtonBackward + tea.MouseButton(code&3)
	case code&64 != 0:
		m.Button = tea.MouseButtonWheelUp + tea.MouseButton(code&3)
	case code&3 == 3:
		m.Button, m.Action = tea.MouseButtonNone, tea.MouseActionRelease
	default:
		m.Button = tea.MouseButtonLeft + tea.MouseButton(code&3)
	}
	if code&32 != 0 && !tea.MouseEvent(m).IsWheel() {
		m.Action = tea.MouseActionMotion
	}
	if m.Action == tea.MouseActionRelease {
		m.Type = tea.MouseRelease
		return m
	}
	if m.Action == tea.MouseActionMotion {
		m.Type = tea.MouseMotion
	}
	switch m.Button {
	case tea.MouseButtonLeft:
		m.Type = tea.MouseLeft
	case tea.MouseButtonMiddle:
		m.Type = tea.MouseMiddle
	case tea.MouseButtonRight:
		m.Type = tea.MouseRight
	case tea.MouseButtonBackward:
		m.Type = tea.MouseBackward
	case tea.MouseButtonForward:
		m.Type = tea.MouseForward
	case tea.MouseButtonWheelUp:
		m.Type = tea.MouseWheelUp
	case tea.MouseButtonWheelDown:
		m.Type = tea.MouseWheelDown
	case tea.MouseButtonWheelLeft:
		m.Type = tea.MouseWheelLeft
	case tea.MouseButtonWheelRight:
		m.Type = tea.MouseWheelRight
	}
	return m
}

func (f *terminalKeyFilter) arm() {
	if f.timer != nil {
		f.timer.Stop()
	}
	f.generation++
	generation := f.generation
	f.timer = time.AfterFunc(terminalEscapeWait, func() { f.send(terminalEscapeTimeout{generation}) })
}

func (f *terminalKeyFilter) flush() []tea.Msg {
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.generation++
	msgs := f.originals
	f.pending, f.originals = "", nil
	return msgs
}

func terminalMessagesMsg(msgs []tea.Msg) tea.Msg {
	switch len(msgs) {
	case 0:
		return nil
	case 1:
		return msgs[0]
	}
	keys := make([]tea.KeyMsg, len(msgs))
	for i, msg := range msgs {
		key, ok := msg.(tea.KeyMsg)
		if !ok {
			return terminalInputBatchMsg(msgs)
		}
		keys[i] = key
	}
	return terminalKeyBatchMsg(keys)
}

// Recognized navigation and function keys overlapping the CPR grammar match the
// upstream decoder. Unknown sequences, Alt text and bracketed paste are preserved.
var terminalNavigationSequences = func() map[string]tea.KeyMsg {
	keys := map[string]tea.KeyMsg{
		"\x1b[1;3R": {Type: tea.KeyF3, Alt: true},
		"\x1b[1;2R": {Type: tea.KeyF15},
		"\x1b[Z":    {Type: tea.KeyShiftTab},
		"\x1b[1~":   {Type: tea.KeyHome}, "\x1b[4~": {Type: tea.KeyEnd},
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
