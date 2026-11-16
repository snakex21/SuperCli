package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitTurns(t *testing.T) {
	got := SplitTurns("first\r\nline two\n\n>>> NEXT USER TURN <<<\n\n second \n  >>> NEXT USER TURN <<<  \n\n")
	if len(got) != 2 || got[0] != "first\nline two" || got[1] != "second" {
		t.Fatalf("SplitTurns = %q", got)
	}
	if got := SplitTurns("only one"); len(got) != 1 || got[0] != "only one" {
		t.Fatalf("single turn = %q", got)
	}
}

func TestComposeTurn(t *testing.T) {
	users := []string{"do A and B", "undo B"}
	answers := []string{"did A and B", ""}
	if ComposeTurn(users, answers, 0) != "do A and B" {
		t.Fatal("turn 1 must be the plain user message")
	}
	p := ComposeTurn(users, answers, 1)
	for _, want := range []string{"do A and B", "did A and B", "undo B", "turn 2", "EARLIER CONVERSATION"} {
		if !strings.Contains(p, want) {
			t.Errorf("turn 2 prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Index(p, "undo B") < strings.Index(p, "did A and B") {
		t.Error("new message must come after the earlier conversation")
	}
}

func TestSumUsage(t *testing.T) {
	a := lastUsage("text\n{\"type\":\"usage\",\"name\":\"run\",\"model\":\"m\",\"input\":10,\"output\":2,\"steps\":3,\"ttftMs\":100,\"ttftAvgMs\":120,\"context\":{\"x\":1}}\n")
	b := lastUsage("{\"type\":\"tool\",\"name\":\"read\"}\n{\"type\":\"usage\",\"name\":\"run\",\"model\":\"m\",\"input\":5,\"output\":1,\"steps\":1,\"ttftMs\":50,\"ttftAvgMs\":60,\"error\":true}\n")
	if a == nil || b == nil {
		t.Fatal("usage lines not found")
	}
	s := SumUsage([]map[string]any{a, b})
	if s["input"] != 15.0 || s["output"] != 3.0 || s["steps"] != 4.0 || s["turns"] != 2.0 {
		t.Fatalf("sums wrong: %v", s)
	}
	if s["ttftMs"] != 100.0 || s["ttftAvgMs"] != 90.0 || s["error"] != true || s["model"] != "m" {
		t.Fatalf("ttft/error/model wrong: %v", s)
	}
	if _, ok := s["context"]; ok {
		t.Fatal("nested objects must be dropped")
	}
}

func TestTaskIDFromDataDir(t *testing.T) {
	ref := t.TempDir()
	for _, id := range []string{"ho3-a", "ho3-a-long", "ho3-b"} {
		if err := os.MkdirAll(filepath.Join(ref, id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ref, id, "solve.py"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"ho3-a-model-r1-123":      "ho3-a",
		"ho3-a-long-model-r2-456": "ho3-a-long",
		"ho3-b-x":                 "ho3-b", // truncated by sanitizeName
	}
	for run, want := range cases {
		got, err := taskIDFromDataDir(ref, filepath.Join("C:\\root", run, "data"))
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", run, got, err, want)
		}
	}
	if _, err := taskIDFromDataDir(ref, filepath.Join("C:\\root", "other-r1-1", "data")); err == nil {
		t.Error("unknown task must fail")
	}
}
