package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// completionLoop registers a file-writing patch_file stand-in and scripts
// the main model: first turn edits `files` files (one call each), every later
// turn replies with the given texts in order.
func completionLoop(t *testing.T, enabled bool, files int, replies ...string) (*Loop, *stubProvider) {
	t.Helper()
	root := t.TempDir()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "patch_file", Description: "edit", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`,
		Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct{ Path string }
			_ = json.Unmarshal(args, &a)
			if err := os.WriteFile(filepath.Join(root, a.Path), []byte("x\n"), 0o644); err != nil {
				return tools.Result{}, err
			}
			return tools.Result{Text: "Patched " + a.Path}, nil
		},
	})
	reg.MarkAlwaysOn("patch_file")
	var scripts [][]llm.Delta
	if files > 0 {
		var calls []llm.Delta
		for i := 0; i < files; i++ {
			calls = append(calls, llm.Delta{ToolCall: &llm.ToolCall{ID: fmt.Sprintf("c%d", i), Name: "patch_file", Arguments: fmt.Sprintf(`{"path":"f%d.txt"}`, i)}})
		}
		calls = append(calls, llm.Delta{FinishReason: "tool_calls"})
		scripts = append(scripts, calls)
	}
	for _, r := range replies {
		scripts = append(scripts, []llm.Delta{{Content: r}, {FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 2, Total: 12}}})
	}
	main := &stubProvider{name: "main", scripts: scripts}
	l, err := NewLoop(LoopConfig{Provider: main, Registry: reg, BaseDir: root, MaxSteps: 10, CompletionCheck: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return l, main
}

func runCompletionLoop(t *testing.T, l *Loop, prompt string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := l.Run(ctx, prompt)
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	done := false
	for ev := range ch {
		switch e := ev.(type) {
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Fatalf("run failed: %v", e.Err)
		case NoticeEvent:
			notices = append(notices, e.Text)
		}
	}
	if !done {
		t.Fatal("no DoneEvent")
	}
	return notices
}

func completionMessages(l *Loop) []string {
	var out []string
	for _, m := range l.Messages {
		if m.Role == llm.RoleSystem && strings.HasPrefix(m.Content, "[completion check]") {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestCompletionCheck_MultiFileRunFinishesNormally(t *testing.T) {
	// The rc1 leftovers trigger for 3+ edited files was dropped (no measured
	// gain, +15 % cost): a finished multi-file Run ends on its answer.
	l, main := completionLoop(t, true, 4, "Renamed in 4 files.")
	runCompletionLoop(t, l, "rename Foo to Bar everywhere")
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	if msgs := completionMessages(l); len(msgs) != 0 {
		t.Fatalf("unexpected completion check: %q", msgs)
	}
}

func TestCompletionCheck_PartialEditsThatAdmitLeftoversGetOnePass(t *testing.T) {
	l, main := completionLoop(t, true, 3, "Added the header to 3 files; the remaining files still need it.", "Done: all files have the header.")
	notices := runCompletionLoop(t, l, "add the header to every file")
	if got := atomic.LoadInt32(&main.calls); got != 3 {
		t.Fatalf("main calls = %d, want 3", got)
	}
	if msgs := completionMessages(l); len(msgs) != 1 {
		t.Fatalf("completion messages = %q", msgs)
	}
	if !strings.Contains(strings.Join(notices, " | "), "admits unfinished work") {
		t.Fatalf("notices = %v", notices)
	}
	if last := l.Messages[len(l.Messages)-1]; last.Role != llm.RoleAssistant || !strings.Contains(messageDraftText(last), "all files") {
		t.Fatalf("final message = %+v", last)
	}
}

func TestCompletionCheck_TypographicApostrophe(t *testing.T) {
	l, main := completionLoop(t, true, 0, "I couldn’t complete it and haven’t changed any files.", "Done what was possible.")
	runCompletionLoop(t, l, "do it")
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
}

func TestCompletionCheck_UnfinishedReplyAsksForTheRest(t *testing.T) {
	l, main := completionLoop(t, true, 0,
		"I can't complete the requested changes because scripts/release.sh is missing. I haven't changed any files.",
		"Done 1 and 2; scripts/release.sh does not exist, so 3 was not done.")
	runCompletionLoop(t, l, "do 1, 2 and 3")
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	msgs := completionMessages(l)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "Do every remaining part that is possible") || !strings.Contains(msgs[0], "Never create, guess or substitute") {
		t.Fatalf("completion messages = %q", msgs)
	}
}

func TestCompletionCheck_HonestPartialAfterEditsFinishes(t *testing.T) {
	l, main := completionLoop(t, true, 1, "Changed f0.txt. I couldn't make the version change: scripts/release.sh does not exist.")
	runCompletionLoop(t, l, "do 1 and 2")
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	if msgs := completionMessages(l); len(msgs) != 0 {
		t.Fatalf("honest partial result re-asked: %q", msgs)
	}
}

func TestCompletionCheck_AtMostOncePerRun(t *testing.T) {
	l, main := completionLoop(t, true, 3, "I did not finish the last file.", "I still did not finish the last file.", "never reached")
	runCompletionLoop(t, l, "edit three files")
	if got := atomic.LoadInt32(&main.calls); got != 3 {
		t.Fatalf("main calls = %d, want 3", got)
	}
	if msgs := completionMessages(l); len(msgs) != 1 {
		t.Fatalf("completion messages = %d, want 1", len(msgs))
	}
	// The next Run starts with a fresh budget.
	l.resetCompletionCheck()
	if l.completion.fired || l.completion.files != nil {
		t.Fatal("reset left state behind")
	}
}

func TestCompletionCheck_DisabledIsFree(t *testing.T) {
	l, main := completionLoop(t, false, 3, "I did not finish.")
	runCompletionLoop(t, l, "edit three files")
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("main calls = %d, want 2", got)
	}
	if msgs := completionMessages(l); len(msgs) != 0 {
		t.Fatalf("disabled check fired: %q", msgs)
	}
}

func TestCompletionCheck_EditFilesChangedPathsCount(t *testing.T) {
	l, _ := completionLoop(t, true, 0)
	l.noteCompletionMutation("edit_files", `{"include":"**/*.go"}`, tools.Result{ChangedPaths: []string{
		filepath.Join(l.baseDir, "a.go"), filepath.Join(l.baseDir, "b.go"), filepath.Join(l.baseDir, "A.go"),
	}})
	l.noteCompletionMutation("patch_file", `{"path":"a.go"}`, tools.Result{})
	// a.go / A.go / ./a.go are one file on case-insensitive volumes.
	if got := len(l.completion.files); got != 2 {
		t.Fatalf("distinct files = %d, want 2 (%v)", got, l.completion.files)
	}
}

func TestUnfinishedReplyPattern(t *testing.T) {
	for _, s := range []string{
		"However, I did not finish the requested headers.",
		"I identified 49 files but I did not make the requested changes.",
		"I haven't made any changes. There are many eligible files.",
		"I can't complete the requested changes because scripts/release.sh is missing.",
		"The task is still incomplete: two packages remain.",
		"Some files still need the header.",
		"Nie udało mi się zmienić wszystkich plików.",
		"Nie wprowadziłem zmian w trzech plikach.",
	} {
		if !unfinishedReply.MatchString(s) {
			t.Errorf("should match: %q", s)
		}
	}
	for _, s := range []string{
		"Renamed userId to accountId in 6 files; tests pass.",
		"scripts/release.sh does not exist, so I could not bump VERSION there; the other two changes are done.",
		"The spec is incomplete and contradictory, so there is nothing to implement.",
		"Gotowe: zmieniono trzy pliki.",
	} {
		if unfinishedReply.MatchString(s) {
			t.Errorf("should not match: %q", s)
		}
	}
}
