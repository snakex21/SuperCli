package memory

import (
	"context"
	"strings"
	"testing"
)

func TestStripReasoning(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "hello world", "hello world"},
		{"closed thinking", "<thinking>secret plan</thinking>visible", "visible"},
		{"closed think", "a <think>x</think> b", "a  b"},
		{"reasoning tag", "<reasoning>blah</reasoning>answer", "answer"},
		{"unclosed (truncated stream)", "summary line\n<thinking>We need to summarize the conversation", "summary line"},
		{"multiline block", "before\n<thinking>\nline1\nline2\n</thinking>\nafter", "before\n\nafter"},
		{"case insensitive", "<THINKING>x</THINKING>ok", "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripReasoning(c.in); got != c.want {
				t.Errorf("StripReasoning(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSplitUserFacts(t *testing.T) {
	in := "Did some work on foo.go\nUSER: The user's name is Maksymilian.\nUSER: Prefers Polish.\nMore summary"
	sum, facts := splitUserFacts(in)
	if strings.Contains(sum, "USER:") {
		t.Errorf("summary still contains USER lines: %q", sum)
	}
	if len(facts) != 2 || facts[0] != "The user's name is Maksymilian." {
		t.Errorf("facts = %#v", facts)
	}
}

// TestFinalizeStripsThinkingAndSavesGlobalFacts is the regression
// test for the observed bug: a task-log entry containing raw
// "<thinking>..." text, and the user's name never reaching the
// global store.
func newStripTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFinalizeStripsThinkingAndSavesGlobalFacts(t *testing.T) {
	project := newStripTestStore(t)
	global := newStripTestStore(t)
	saver := &AutoSaver{Project: project, Global: global, ProjectPath: t.TempDir()}

	var gotPrompt string
	summarize := func(ctx context.Context, prompt string) (string, error) {
		gotPrompt = prompt
		return "<thinking>We need to summarize the session.</thinking>" +
			"Discussed greetings; no files touched.\n" +
			"USER: The user's name is Maksymilian.", nil
	}
	saver.Finalize(context.Background(), "user: cześć mam na imię Maksymilian\n<thinking>internal chain of thought</thinking>assistant: cześć!", summarize)

	if strings.Contains(gotPrompt, "<thinking>") {
		t.Errorf("summarize prompt still contains thinking block:\n%s", gotPrompt)
	}
	logs, err := project.Recent(ScopeTaskLog, 5)
	if err != nil || len(logs) != 1 {
		t.Fatalf("task logs = %v, err %v", logs, err)
	}
	if strings.Contains(logs[0].Content, "<thinking>") || strings.Contains(logs[0].Content, "USER:") {
		t.Errorf("task log content not cleaned: %q", logs[0].Content)
	}
	prefs, err := global.Recent(ScopePreference, 5)
	if err != nil || len(prefs) != 1 {
		t.Fatalf("global preferences = %v, err %v", prefs, err)
	}
	if !strings.Contains(prefs[0].Content, "Maksymilian") {
		t.Errorf("global preference = %q, want the user's name", prefs[0].Content)
	}

	// Dedup: a second session producing the same fact must not
	// create a duplicate entry.
	saver2 := &AutoSaver{Project: project, Global: global, ProjectPath: t.TempDir()}
	saver2.Finalize(context.Background(), "user: hej\nassistant: hej", func(ctx context.Context, prompt string) (string, error) {
		return "Small talk only.\nUSER: The user's name is Maksymilian", nil
	})
	prefs, _ = global.Recent(ScopePreference, 5)
	if len(prefs) != 1 {
		t.Errorf("dedup failed: %d preference entries", len(prefs))
	}
}
