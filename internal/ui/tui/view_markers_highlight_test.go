package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestToolFileLang(t *testing.T) {
	for _, tc := range []struct{ name, args, want string }{
		{"read_lines", `{"file":"internal/a.go","from":1}`, "go"},
		{"read_context", `{"file":"C:\\x\\app.CSS"}`, "css"},
		{"read_lines", `{"file":"Makefile"}`, ""},
		{"ctx_execute", `{"file":"a.go"}`, ""},
		{"read_lines", `{bad`, ""},
		{"patch_file", `{"path":"x/y.js","old":"a","new":"b"}`, "js"},
	} {
		if got := toolFileLang(tc.name, tc.args); got != tc.want {
			t.Fatalf("%s %s: got %q want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestToolResultFullHighlightsFileReads(t *testing.T) {
	r := lipgloss.NewRenderer(nil)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	m := NewMarker(NewPalette(r))
	plainMarker := NewMarker(NoColorPalette())

	read := "   1 | package main\n   2 | func main() {}\n"
	colored := m.toolResultFull("read_lines", read, true, "go")
	plain := plainMarker.toolResultFull("read_lines", read, true, "go")
	if colored == m.toolResultFull("read_lines", read, true, "") {
		t.Fatal("go file read was not highlighted")
	}
	if ansi.Strip(colored) != ansi.Strip(plain) {
		t.Fatalf("highlighting changed visible text:\n%q\n%q", ansi.Strip(colored), ansi.Strip(plain))
	}

	many := "== [1] a.go:1-1 ==\n   1 | package main\n[read_many: 1 ok, 0 failed]\n"
	if got := m.toolResultFull("read_many", many, true, ""); !strings.Contains(ansi.Strip(got), "1 | package main") ||
		got == plainMarker.toolResultFull("read_many", many, true, "") {
		t.Fatalf("read_many section language not applied: %q", got)
	}
	patched := "Patched y.go: replaced 1\n[Written snapshot]\n   1 | package main\n[end of file at line 1]"
	if got := m.toolResultFull("patch_file", patched, true, "go"); got == m.toolResultFull("patch_file", patched, true, "") ||
		ansi.Strip(got) != ansi.Strip(plainMarker.toolResultFull("patch_file", patched, true, "go")) {
		t.Fatalf("patch_file snapshot not highlighted cleanly: %q", got)
	}
	// Non-read tools get no code colours.
	if m.toolResultFull("ctx_execute", read, true, "go") != m.ToolResultFull("ctx_execute", read, true) {
		t.Fatal("non-read tool output was highlighted")
	}
	// Test-log status lines are coloured without changing their text.
	log := "ok  \tsupercli/x\t1.2s\nsome line\nFAIL\tsupercli/y\n"
	gotLog := m.ToolResultFull("ctx_execute", log, true)
	if !strings.Contains(gotLog, m.p.Success.Render("ok  \tsupercli/x\t1.2s")) ||
		!strings.Contains(gotLog, m.p.ToolErr.Render("FAIL\tsupercli/y")) ||
		ansi.Strip(gotLog) != ansi.Strip(plainMarker.ToolResultFull("ctx_execute", log, true)) {
		t.Fatalf("status lines not coloured cleanly: %q", gotLog)
	}
}
