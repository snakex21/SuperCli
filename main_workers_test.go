package main

import (
	"strings"
	"testing"

	"supercli/internal/agent"
)

func TestFormatWorkers_Empty(t *testing.T) {
	out := formatWorkers(agent.NewWorkerRegistry())
	if !strings.Contains(out, "none yet") {
		t.Errorf("empty registry: want a 'none yet' message, got:\n%s", out)
	}
}

func TestFormatWorkers_NilRegistry(t *testing.T) {
	// reg.List() is nil-safe; formatWorkers must not panic on a nil registry.
	out := formatWorkers(nil)
	if !strings.Contains(out, "none yet") {
		t.Errorf("nil registry: want a 'none yet' message, got:\n%s", out)
	}
}

func TestFormatWorkers_Populated(t *testing.T) {
	reg := agent.NewWorkerRegistry()

	w1 := reg.Add("explore", "find where slash commands are dispatched", nil)
	w1.Status = "done"
	w1.TokensIn = 1200
	w1.TokensOut = 340

	w2 := reg.Add("code", "implement the /context report formatter", nil)
	w2.Status = "running"

	w3 := reg.Add("code", "broken task", nil)
	w3.Status = "failed"
	w3.LastError = "boom: provider returned 500"

	out := formatWorkers(reg)

	for _, want := range []string{
		"Workers:",
		"worker-1", "explore", "done",
		"worker-2", "code", "running",
		"worker-3", "failed",
		"1200 in / 340 out tok",
		"find where slash commands are dispatched",
		"boom: provider returned 500",
		"/workers stop <id>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted workers missing %q:\n%s", want, out)
		}
	}

	// A done worker's error line must not be printed even if LastError is set.
	w1.LastError = "stale error should be hidden for done workers"
	out = formatWorkers(reg)
	if strings.Contains(out, "stale error should be hidden") {
		t.Errorf("done worker should not show its LastError:\n%s", out)
	}

	// Workers without token usage should not render a token suffix.
	if strings.Contains(out, "0 in / 0 out tok") {
		t.Errorf("zero-usage worker should omit the token suffix:\n%s", out)
	}
}
