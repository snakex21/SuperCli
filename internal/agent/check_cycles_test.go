package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// The S5 regression (impossible-spec): the model switched Pick between two
// versions, each failing the contradictory tests with the same output. The
// repeat of an already failed version is now named in the test result, and
// after three repeats the Run is steered to a tool-free explanation.
func TestFlipFloppingEditsAreNamedAndStopped(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pick.go")
	if err := os.WriteFile(path, []byte("func Pick() { return first }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.NewPatchFile(root).Spec())
	reg.MarkAlwaysOn("patch_file")
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}},"required":["command"]}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "largest") {
			return tools.Result{Err: errors.New("--- FAIL: TestPickFirst (0.00s)\n    want first\nFAIL\tpick\t0.012s")}, nil
		}
		return tools.Result{Err: errors.New("--- FAIL: TestPickLargest (0.01s)\n    want largest\nFAIL\tpick\t0.034s")}, nil
	}})
	reg.MarkAlwaysOn("ctx_execute")

	flip := func(id, from, to string) []llm.Delta {
		args, _ := json.Marshal(map[string]string{"path": "pick.go", "old": "return " + from, "new": "return " + to})
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: "patch_file", Arguments: string(args)}}}
	}
	test := func(id string) []llm.Delta {
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}}}
	}
	var scripts [][]llm.Delta
	for i := 0; i < 5; i++ {
		from, to := "first", "largest"
		if i%2 == 1 {
			from, to = to, from
		}
		scripts = append(scripts, flip(fmt.Sprintf("p%d", i), from, to), test(fmt.Sprintf("t%d", i)))
	}
	scripts = append(scripts, []llm.Delta{{Content: "The tests contradict each other: TestPickFirst wants the first element, TestPickLargest the largest.", FinishReason: "stop"}})
	provider := &stubProvider{name: "flip-flop", scripts: scripts}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, MaxSteps: 20})
	if err != nil {
		t.Fatal(err)
	}
	var notices, streamed strings.Builder
	for _, event := range drainEvents(t, mustRun(t, loop, "Make the tests pass.")) {
		switch e := event.(type) {
		case NoticeEvent:
			notices.WriteString(e.Text + "\n")
		case MessageEvent:
			streamed.WriteString(e.Text)
		case ErrorEvent:
			t.Fatalf("run aborted before the cycle cap: %v", e.Err)
		}
	}
	results := map[string]string{}
	for _, message := range loop.Messages {
		if message.Role == llm.RoleTool {
			results[message.ToolCallID] = message.Content
		}
	}
	for id, wantNote := range map[string]string{"t0": "", "t1": "", "t2": "step 2 with identical code (now 2 times)", "t3": "step 4 with identical code (now 2 times)", "t4": "step 4 with identical code (now 3 times)"} {
		got := results[id]
		if wantNote == "" {
			if strings.Contains(got, "[loop]") {
				t.Fatalf("%s: first failure of a version flagged: %s", id, got)
			}
			continue
		}
		if !strings.Contains(got, "[loop] Same failure as at step") || !strings.Contains(got, wantNote) {
			t.Fatalf("%s: want note %q, got %s", id, wantNote, got)
		}
		if !strings.Contains(got, "Change approach: re-read the failing assertion") {
			t.Fatalf("%s: note does not steer to a new approach: %s", id, got)
		}
	}
	if !strings.Contains(notices.String(), "SuperCli stopped the edit loop") {
		t.Fatalf("user not told: %s", notices.String())
	}
	// The request after the cap carries no tools: the model can only answer.
	if last := provider.toolReqs[len(provider.toolReqs)-1]; last != 0 {
		t.Fatalf("tools still offered after the cap: %d", last)
	}
	if !strings.Contains(streamed.String(), "SuperCli stopped the edit loop") {
		t.Fatalf("final answer lacks the stop note: %s", streamed.String())
	}
}

// (p4 repeats p0/p2 verbatim, so the identical-write gate blocks it and t4
// retests the version from t1/t3: still a repeat.)

// Distinct versions failing differently are ordinary iteration, never flagged.
func TestCheckCyclesIgnoresNewVersionsAndPassingRuns(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	var c checkCycles
	c.touch(root, `{"path":"a.go"}`)
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("v%d", i)), 0o600)
		c.observe(fmt.Sprint(i), []string{"go", "test"}, true, "FAIL x 0.01s", int64(i))
		if note := c.note(fmt.Sprint(i)); note != "" {
			t.Fatalf("new version %d flagged: %s", i, note)
		}
	}
	_ = os.WriteFile(path, []byte("v0"), 0o600)
	c.observe("pass", []string{"go", "test"}, false, "ok", 9)
	if c.note("pass") != "" {
		t.Fatal("passing run flagged")
	}
	// Timing noise does not hide a repeat.
	c.observe("again", []string{"go", "test"}, true, "FAIL x 0.93s", 10)
	if !strings.Contains(c.note("again"), "as at step 0 ") {
		t.Fatal("repeat with different timing not detected")
	}
	if _, due := c.stopDue(); due {
		t.Fatal("cap reached after one repeat")
	}
}

// The loop texts must not hand the model a ready-made excuse: a hint that
// "the tests may contradict each other" made models abandon solvable tasks.
// A conflict is only named when the model can say precisely which
// requirements clash, and the stop message stays neutral.
func TestCheckCycleTextsDoNotSuggestGivingUp(t *testing.T) {
	note := fmt.Sprintf(checkCycleNoteText, 3, 2)
	for _, want := range []string{"Same failure as at step 3 with identical code", "Change approach", "re-read the failing assertion", "explain precisely which ones"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q: %s", want, note)
		}
	}
	stop := fmt.Sprintf(checkCycleStopText, 3)
	for _, text := range []string{note, stop} {
		lower := strings.ToLower(text)
		for _, bad := range []string{"may contradict", "contradict each other", "tests may"} {
			if strings.Contains(lower, bad) {
				t.Errorf("loop text suggests the task is impossible (%q): %s", bad, text)
			}
		}
	}
	if !strings.HasPrefix(stop, "SuperCli stopped the edit loop") {
		t.Errorf("stop text changed its user-visible prefix: %s", stop)
	}
}
