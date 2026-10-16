package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/webgui/qrcode"
)

type phoneState struct {
	session           *phoneSession
	launch            *phoneLaunchers
	url               string
	code              *qrcode.Code
	consent           bool
	status            string
	detail            string
	downloaded, total int64
}

func (m Model) openPhoneMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuPhone})
	if m.phone.session == nil && !m.phone.consent {
		return m.startPhone(false)
	}
	return m, nil
}

func (m Model) startPhone(allow bool) (tea.Model, tea.Cmd) {
	launch := defaultPhoneLaunchers()
	if m.phone.launch != nil {
		launch = *m.phone.launch
	}
	m.phone.session = newPhoneSession()
	m.phone.url, m.phone.detail = "", ""
	m.phone.code = nil
	m.phone.consent = false
	m.phone.downloaded, m.phone.total = 0, 0
	m.phone.status = "phone.starting"
	return m, launchPhoneCmd(m.phone.session, m.home, m.dataDir, allow, launch)
}

func (m Model) handlePhoneKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := strings.ToLower(msg.String())
	switch key {
	case "esc", "ctrl+k":
		return m.closeMenu()
	case "t", "y":
		if m.phone.consent {
			// The Polish prompt uses t; the English prompt uses y.
			if (m.language == "pl" && key != "t") || (m.language != "pl" && key != "y") {
				return m, nil
			}
			return m.startPhone(true)
		}
	case "r":
		if m.phone.session != nil {
			m.phone.session.cancel()
		}
		m.phone.session = nil
		m.phone.url, m.phone.detail = "", ""
		m.phone.code = nil
		m.phone.consent = false
		m.phone.status = "phone.off"
	case "n":
		if m.phone.consent {
			m.phone.consent = false
			m.phone.status = "phone.off"
		} else if s := m.phone.session; s != nil && m.phone.url != "" {
			// A bounded queue prevents key repeat from blocking Update.
			select {
			case s.commands <- "pair":
			default:
			}
		} else if m.phone.session == nil {
			return m.startPhone(false)
		}
	case "enter":
		if m.phone.consent {
			m.phone.consent = false
			m.phone.status = "phone.off"
		}
	}
	return m, nil
}

func (m Model) applyPhoneEvent(msg phoneEventMsg) (tea.Model, tea.Cmd) {
	if m.phone.session != msg.session {
		return m, nil
	}
	e := msg.event
	switch e.kind {
	case "SUPERCLI_PHONE_NEED_DOWNLOAD":
		m.phone.session = nil
		m.phone.consent = true
		m.phone.total = e.total
		m.phone.status = "phone.download"
	case "SUPERCLI_PHONE_PROGRESS":
		m.phone.downloaded, m.phone.total = e.done, e.total
		m.phone.status = "phone.progress"
	case "SUPERCLI_PHONE_URL":
		code, err := qrcode.Encode(e.text)
		if err != nil {
			m.phone.status = "phone.failed"
			m.phone.detail = err.Error()
			break
		}
		m.phone.url, m.phone.code = e.text, code
		m.phone.status, m.phone.detail = "phone.ready", ""
	case "SUPERCLI_PHONE_CLOSED", "exit", "error":
		msg.session.cancel()
		m.phone.session = nil
		m.phone.url, m.phone.code = "", nil
		m.phone.status, m.phone.detail = "phone.off", e.text
		if e.err != nil {
			m.phone.status, m.phone.detail = "phone.failed", e.err.Error()
		}
		m.setStatus(m.tr("phone.title")+": "+m.tr(m.phone.status)+" "+m.phone.detail, e.err == nil)
		return m, m.statusClearCmd()
	}
	if m.phone.session == nil {
		return m, nil
	}
	return m, waitPhoneEvent(msg.session)
}

// QR colors deliberately bypass the palette and NO_COLOR: inverse terminal
// themes must never turn the quiet zone dark. Each cell holds two modules.
func renderPhoneQR(code *qrcode.Code) string {
	const quiet = 4
	size := code.Size + 2*quiet
	dark := func(x, y int) bool {
		return x >= quiet && y >= quiet && x < size-quiet && y < size-quiet && code.Dark(x-quiet, y-quiet)
	}
	var b strings.Builder
	for y := 0; y < size; y += 2 {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("\x1b[0;38;2;0;0;0;48;2;255;255;255m")
		for x := 0; x < size; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// A dedicated full-width view preserves the complete copyable link rather than
// letting the generic menu frame truncate either the QR or its quiet zone.
func (m Model) renderPhoneView() string {
	width := maxInt(1, m.width)
	wrap := func(s string) string { return ansi.Hardwrap(s, width, true) }
	status := m.tr("phone.off")
	if m.phone.status != "" {
		status = m.tr(m.phone.status)
	}
	if m.phone.detail != "" {
		status += " " + m.phone.detail
	}
	if m.phone.status == "phone.progress" {
		cells := minInt(24, maxInt(1, width-2))
		filled := 0
		if m.phone.total > 0 {
			filled = int(float64(m.phone.downloaded) / float64(m.phone.total) * float64(cells))
		}
		status += fmt.Sprintf("\n[%s%s] %d / %d B", strings.Repeat("█", filled), strings.Repeat("░", cells-filled), m.phone.downloaded, m.phone.total)
	}
	footer := wrap(status) + "\n" + wrap(m.tr("phone.hint"))
	link := ""
	if m.phone.url != "" {
		link = wrap(m.phone.url) + "\n"
	}
	title := wrap(m.tr("phone.title")) + "\n"
	body := ""
	if m.phone.code != nil {
		size := m.phone.code.Size + 8
		needed := strings.Count(title+link+footer, "\n") + 1 + (size+1)/2
		if width < size || m.height < needed {
			body = wrap(m.tr("phone.small")) + "\n"
		} else {
			body = renderPhoneQR(m.phone.code) + "\n"
		}
	}
	return title + body + link + footer
}

// Delay Tea's quit only for the owned helper's bounded graceful shutdown. The
// cancellation is synchronous so a pending launcher is also covered.
func (m Model) quitPhoneCmd() tea.Cmd {
	s := m.phone.session
	if s == nil {
		return tea.Quit
	}
	s.cancel()
	return func() tea.Msg { <-s.done; return tea.Quit() }
}
