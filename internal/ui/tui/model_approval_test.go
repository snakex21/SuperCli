package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/tools"
)

func approvalModel(t *testing.T, question string, similar bool) (Model, chan tools.AskAnswer) {
	t.Helper()
	req := sampleRequest()
	req.Question = question
	req.Header = "Approval required"
	req.Confirmation = true
	req.Approval = &tools.ApprovalCard{Tool: "thunderbird_mail", Operation: "copy", ApproveLabel: "Approve", DenyLabel: "Deny"}
	req.Options = []tools.AskOption{{Label: "Deny"}, {Label: "Approve"}}
	if similar {
		req.Approval.SimilarLabel = "Approve similar in this session"
		req.Options = append(req.Options, tools.AskOption{Label: "Approve similar in this session"})
	}
	m := New(Options{Home: t.TempDir()})
	m.width, m.height = 80, 24
	out, _ := m.beginAsk(req)
	return out.(Model), req.Respond
}

func approvalKey(m Model, key string) Model {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	if key == "esc" {
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	}
	out, _ := m.handleAskKey(msg)
	return out.(Model)
}

func TestApprovalPromptYNA(t *testing.T) {
	for key, want := range map[string]tools.AskAnswer{
		"y":   {Selected: []string{"Approve"}},
		"a":   {Selected: []string{"Approve similar in this session"}},
		"n":   {Cancelled: true},
		"esc": {Cancelled: true},
	} {
		m, ch := approvalModel(t, "Copy 2 messages\nAccount: Local Folders", true)
		view := m.View()
		for _, s := range []string{"[y] Approve", "[n] Deny", "[a] Approve similar", "Local Folders"} {
			if !strings.Contains(view, s) {
				t.Fatalf("%s: view lacks %q:\n%s", key, s, view)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > 80 {
				t.Fatalf("width overflow %q", line)
			}
		}
		m = approvalKey(m, key)
		got := <-ch
		if got.Cancelled != want.Cancelled || strings.Join(got.Selected, ",") != strings.Join(want.Selected, ",") {
			t.Fatalf("%s: got %+v want %+v", key, got, want)
		}
		if m.pendingAsk != nil {
			t.Fatalf("%s: prompt still open", key)
		}
	}
}

func TestApprovalPromptNeedsDetailsReadAndHasNoSimilarForIrreversible(t *testing.T) {
	long := strings.Repeat("detail line\n", 60) + "LAST"
	m, ch := approvalModel(t, long, false)
	if strings.Contains(m.View(), "[a]") {
		t.Fatal("irreversible prompt offers a session grant")
	}
	m = approvalKey(m, "y")
	m = approvalKey(m, "a")
	if m.pendingAsk == nil || len(ch) != 0 {
		t.Fatal("approved before the details were read")
	}
	out, _ := m.handleAskKey(tea.KeyMsg{Type: tea.KeyEnd})
	m = approvalKey(out.(Model), "y")
	if got := <-ch; len(got.Selected) != 1 || got.Selected[0] != "Approve" {
		t.Fatalf("after reading: %+v", got)
	}
}
