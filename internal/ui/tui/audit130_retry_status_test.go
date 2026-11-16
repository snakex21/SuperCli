package tui

// audit-130: provider retry/backoff (429/503) notices. The llm layer
// documents them as the "user-visible status line" (llm.rateLimitNotice);
// the TUI receives them as agent.NoticeEvent.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/agent"
)

// Wire formats copied from llm.rateLimitNotice / agent stream_retry.go.
var audit130RetryNotices = []struct{ wire, plWant string }{
	{"gpt-5: rate limited (HTTP 429) — retrying in 2s (retry 1/5)", "limit zapytań (HTTP 429) — ponawianie za 2s (ponowienie 1/5)"},
	{"gpt-5: provider error (HTTP 503) — retrying in 4s (retry 2/5)", "błąd dostawcy AI (HTTP 503) — ponawianie za 4s (ponowienie 2/5)"},
	{"gpt-5: temporary provider error (HTTP 408) — retrying in 1s (retry 3/∞)", "(HTTP 408) — ponawianie za 1s (ponowienie 3/∞)"},
	{"connection to the model dropped (EOF) — retrying in 1.5s (retry 1/5)", "ponawianie za 1.5s (ponowienie 1/5)"},
	{"Model connection unavailable — waiting 30s before retry 2/5.", "czekam 30s przed ponowieniem 2/5."},
}

func audit130BusyModel(t *testing.T, lang string, w, h int) Model {
	t.Helper()
	dir := t.TempDir()
	m := New(Options{Home: dir, DataDir: dir, Language: lang})
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = out.(Model)
	m.busy = true
	m.refreshTranscript()
	return m
}

func audit130Event(m Model, ev agent.Event) Model {
	out, _ := m.handleAgentEvent(ev)
	return out.(Model)
}

// statusRows returns the rows between the separator rule and the composer.
func audit130StatusRows(view string) []string {
	lines := strings.Split(ansi.Strip(view), "\n")
	var rows []string
	inStatus := false
	for _, l := range lines {
		if strings.HasPrefix(l, "────") {
			inStatus = true
			continue
		}
		if inStatus {
			if strings.HasPrefix(strings.TrimSpace(l), "╭") {
				break
			}
			rows = append(rows, l)
		}
	}
	return rows
}

// Every retry notice is localized in the transcript (pl) - works today.
func TestAudit130RetryNoticesLocalizedInTranscript(t *testing.T) {
	for _, n := range audit130RetryNotices {
		m := audit130BusyModel(t, "pl", 120, 30)
		m = audit130Event(m, agent.NoticeEvent{Text: n.wire})
		text := audit130Transcript(m)
		if !strings.Contains(text, n.plWant) {
			t.Errorf("notice %q not localized; transcript:\n%s", n.wire, text)
		}
		for _, english := range []string{"retrying in", "rate limited", "before retry"} {
			if strings.Contains(text, english) {
				t.Errorf("English %q left in pl transcript for %q", english, n.wire)
			}
		}
	}
}

// The live status row (under the rule) should say the run is waiting on a
// retry, and stop saying so once tokens stream again.
func TestAudit130RetryStateInStatusLineAndCleared(t *testing.T) {
	m := audit130BusyModel(t, "en", 120, 30)
	m = audit130Event(m, agent.NoticeEvent{Text: audit130RetryNotices[0].wire})
	status := strings.Join(audit130StatusRows(m.View()), "\n")
	if !strings.Contains(status, "429") && !strings.Contains(strings.ToLower(status), "retry") {
		t.Fatalf("status rows do not show the retry state:\n%s", status)
	}
	m = audit130Event(m, agent.MessageEvent{Text: "hello"})
	status = strings.Join(audit130StatusRows(m.View()), "\n")
	if strings.Contains(status, "429") || strings.Contains(strings.ToLower(status), "retry") {
		t.Fatalf("retry state not cleared after streaming resumed:\n%s", status)
	}
}

// Repeated retry notices of one outage are coalesced into one live
// transcript row, so max_retries = -1 (∞) cannot flood the conversation.
func TestAudit130RetryNoticesAccumulateInTranscript(t *testing.T) {
	m := audit130BusyModel(t, "en", 120, 30)
	before := len(m.chat.msgs)
	for i := 0; i < 20; i++ {
		m = audit130Event(m, agent.NoticeEvent{Text: "gpt-5: rate limited (HTTP 429) — retrying in 2s (retry 1/∞)"})
	}
	added := len(m.chat.msgs) - before
	t.Logf("20 retry notices added %d transcript messages", added)
	m = audit130Event(m, agent.MessageEvent{Text: "answer"})
	m = audit130Event(m, agent.DoneEvent{})
	if text := audit130Transcript(m); !strings.Contains(text, "answer") {
		t.Fatalf("answer missing after retries:\n%s", text)
	}
	if added > 1 {
		t.Fatalf("20 identical retry notices produced %d transcript rows; expected them to be coalesced into one live row", added)
	}
}

// The notice handler does not repaint by itself; the 16 ms stream flush tick
// must paint it while the run waits on the backoff (no other events arrive).
func TestAudit130RetryNoticePaintedByFlushTick(t *testing.T) {
	m := audit130BusyModel(t, "pl", 120, 30)
	ch := make(chan agent.Event)
	m.eventCh = ch
	m = audit130Event(m, agent.NoticeEvent{Text: audit130RetryNotices[0].wire})
	out, _ := m.Update(streamFlushMsg{})
	m = out.(Model)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "ponawianie za 2s") {
		t.Fatalf("retry notice not painted during the backoff wait:\n%s", view)
	}
}

// A later outage (after the stream resumed) gets its own row; the earlier
// outage row is kept as history.
func TestAudit130RetryNewOutageStartsNewRow(t *testing.T) {
	m := audit130BusyModel(t, "en", 120, 30)
	before := len(m.chat.msgs)
	m = audit130Event(m, agent.NoticeEvent{Text: audit130RetryNotices[0].wire})
	m = audit130Event(m, agent.NoticeEvent{Text: audit130RetryNotices[1].wire})
	if got := len(m.chat.msgs) - before; got != 1 {
		t.Fatalf("one outage produced %d rows", got)
	}
	if last := m.chat.msgs[len(m.chat.msgs)-1].text; !strings.Contains(last, "HTTP 503") {
		t.Fatalf("live retry row not updated to the latest notice: %q", last)
	}
	m = audit130Event(m, agent.MessageEvent{Text: "partial"})
	m = audit130Event(m, agent.NoticeEvent{Text: audit130RetryNotices[3].wire})
	if got := len(m.chat.msgs) - before; got != 2 {
		t.Fatalf("second outage should add a second row, got %d rows", got)
	}
}
