package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/tools"
)

func trashApprovalModel(t *testing.T, language string, width, height int, detail string) (Model, chan tools.AskAnswer) {
	t.Helper()
	req := sampleRequest()
	req.Question = "fallback question"
	req.Header = "Wymagana zgoda"
	req.Confirmation = true
	req.Approval = &tools.ApprovalCard{Tool: "trash", Operation: "trash_outside_workspace", Risk: "write_reversible", Reversible: true,
		Target: `C:\Users\demo\outside\old_notes.txt`, Summary: "Remove it?" + detail,
		SummaryKey: "approval.summary.trash_outside_workspace", SummaryArgs: map[string]string{"path": `C:\Users\demo\outside\old_notes.txt`},
		SummaryDetail: strings.TrimPrefix(detail, "\n"),
		ApproveLabel:  "Zatwierdź", DenyLabel: "Odrzuć", SimilarLabel: "Zatwierdź podobne"}
	req.Options = []tools.AskOption{{Label: "Odrzuć"}, {Label: "Zatwierdź"}, {Label: "Zatwierdź podobne"}}
	m := New(Options{Home: t.TempDir(), Language: language})
	m.width, m.height = width, height
	m.chat.addAssistant("earlier answer line ALPHA")
	m.resizeViewport()
	m.refreshTranscript()
	out, _ := m.beginAsk(req)
	return out.(Model), req.Respond
}

func TestApprovalCardIsLocalizedRendersFieldsAndKeepsTranscriptTail(t *testing.T) {
	m, ch := trashApprovalModel(t, "pl", 100, 34, "")
	view := ansi.Strip(m.View())
	for _, want := range []string{
		"Usunąć plik spoza obszaru roboczego?", // approval.op.* heading in Polish
		"ZMIANA", "ODWRACALNE",                 // risk and reversibility badges
		"Obiekt", `old_notes.txt`, "Narzędzie", "trash / trash_outside_workspace",
		"[n] Odrzuć", "[y] Zatwierdź", "[a] Zatwierdź podobne",
		"szczegóły", // localized key hint
		"ALPHA",     // transcript tail stays visible above the card
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	for _, english := range []string{"Approval required (y/n/a)", "Details ", "(read to end)", "Read to end"} {
		if strings.Contains(view, english) {
			t.Fatalf("untranslated chrome %q:\n%s", english, view)
		}
	}
	lines := strings.Split(view, "\n")
	if len(lines) != 34 {
		t.Fatalf("view has %d lines, want the full terminal", len(lines))
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 100 {
			t.Fatalf("width overflow %q", line)
		}
	}
	// Card height follows content: far fewer rows than the terminal.
	top := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "╭─ ⏸") {
			top = i
		}
	}
	if top < 15 {
		t.Fatalf("card starts at row %d; it should leave room for the transcript", top)
	}
	// Deny stays the default: Enter right away denies.
	out, _ := m.handleAskKey(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-ch; !got.Cancelled {
		t.Fatalf("Enter did not deny: %+v", got)
	}
	_ = out
}

func TestApprovalCardLongDetailsStillNeedReadingToEnd(t *testing.T) {
	detail := "\n" + strings.Repeat("argument line\n", 80) + "LAST-LINE"
	m, ch := trashApprovalModel(t, "pl", 80, 24, detail)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "(przeczytaj do końca)") || !strings.Contains(view, "Szczegóły 1–") {
		t.Fatalf("scroll state not shown:\n%s", view)
	}
	if len(strings.Split(view, "\n")) > 24 {
		t.Fatal("height overflow")
	}
	m = approvalKey(m, "y")
	m = approvalKey(m, "a")
	if m.pendingAsk == nil || len(ch) != 0 {
		t.Fatal("approved before the details were read")
	}
	out, _ := m.handleAskKey(tea.KeyMsg{Type: tea.KeyEnd})
	m = out.(Model)
	if !strings.Contains(ansi.Strip(m.View()), "LAST-LINE") {
		t.Fatal("last detail line not reachable")
	}
	m = approvalKey(m, "y")
	if got := <-ch; len(got.Selected) != 1 || got.Selected[0] != "Zatwierdź" {
		t.Fatalf("after reading: %+v", got)
	}
}

func TestApprovalCardTinyTerminalDeniesOnly(t *testing.T) {
	m, ch := trashApprovalModel(t, "pl", 30, 9, "")
	if view := m.View(); !strings.Contains(view, "24x10") {
		t.Fatalf("resize hint missing: %q", view)
	}
	m = approvalKey(m, "y")
	if m.pendingAsk == nil {
		t.Fatal("tiny terminal approved")
	}
	approvalKey(m, "esc")
	if got := <-ch; !got.Cancelled {
		t.Fatalf("Esc must deny: %+v", got)
	}
}
