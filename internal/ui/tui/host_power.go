package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"supercli/internal/system/hostpower"
	"supercli/internal/tools"
)

// Substitution is single-pass: goal text containing {seconds} or {goal} is
// literal user content, never another template or terminal markup.
func localizedText(text string, args map[string]string) string {
	return strings.NewReplacer("{goal}", args["goal"], "{seconds}", args["seconds"]).Replace(text)
}

func (m Model) trustedQuestion(req tools.AskRequest) string {
	if req.Confirmation && req.QuestionKey == "hostPower.confirm" {
		if text := m.tr(req.QuestionKey); text != req.QuestionKey {
			return localizedText(text, req.QuestionArgs)
		}
	}
	return req.Question
}

func hostPowerIdentity(s hostpower.Snapshot) string {
	return fmt.Sprintf("%s:%s:%s:%d:%s", s.JobID, s.GoalID, s.Status, s.Deadline, s.Error)
}

// The existing spinner frame lifecycle continues even after terminal host
// states. Reuse it to notice later Arm calls while tools run, with no new poll
// timer, waiting goroutine or busy loop. Snapshots never execute host actions.
func (m *Model) refreshHostPower() {
	if m.hostPower == nil {
		return
	}
	next := m.hostPower.Snapshot()
	if next == m.hostPowerState {
		return
	}
	m.hostPowerState = next
	m.resizeViewport()
}

func hostPowerText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m Model) renderHostPower() string {
	s := m.hostPowerState
	switch s.Status {
	case "armed", "countdown", "cancelled", "blocked", "executing", "failed", "executed":
	default:
		return ""
	}
	if hostPowerIdentity(s) == m.hostPowerDismissed {
		return ""
	}
	seconds := max(int64(0), (s.Deadline-time.Now().UnixMilli()+999)/1000)
	text := m.tr("hostPower.title") + ": " + localizedText(m.tr("hostPower."+s.Status), map[string]string{"seconds": fmt.Sprint(seconds)})
	// Bound footer height independently of goal/path length. Always reserve the
	// Escape hint before optional goal text, even on small terminals.
	width := max(1, m.width)
	limit := max(2, min(5, m.height/3))
	hint := []string(nil)
	if s.Status == "armed" || s.Status == "countdown" {
		hint = wrap(m.tr("hostPower.escape"), width)
	}
	rows := wrap(text, width)
	available := max(1, limit-len(hint))
	if len(rows) > available {
		rows = rows[:available]
	}
	rows = append(rows, hint...)
	if s.Goal != "" && m.height >= 18 && len(rows) < limit {
		rows = append(rows, truncateVisible(hostPowerText(s.Goal), width))
	}
	return m.palette.HeaderMode.Render(strings.Join(rows, "\n"))
}
