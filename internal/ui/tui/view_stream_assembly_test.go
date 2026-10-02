package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestStreamAssemblyExactBytesAndCleanup(t *testing.T) {
	cases := []struct{ name, prompt, current, spinner, want string }{
		{"empty", "", "", "", ""},
		{"spinner only", "", "", "spin", "spin\n"},
		{"first answer", "", "Answer.", "spin", "SuperCli\n▌ Answer. spin\n"},
		{"answer without spinner", "", "Answer.", "", "SuperCli\n▌ Answer. \n"},
		{"history only", "Prompt", "", "", "You\n▌ Prompt\n"},
		{"history spinner", "Prompt", "", "spin", "You\n▌ Prompt\nspin\n"},
		{"history answer", "Prompt", "Answer.", "spin", "You\n▌ Prompt\n\nSuperCli\n▌ Answer. spin\n"},
		{"unicode", "Żółć", "中文 😀", "⠸", "You\n▌ Żółć\n\nSuperCli\n▌ 中文 😀 ⠸\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newChat(80, "en")
			c.legacySymbols = false
			if tc.prompt != "" {
				c.addUser(tc.prompt)
			}
			c.current = tc.current
			c.activeCache = &activeSectionCache{}
			if got := c.renderWithSpinner(NoColorPalette(), tc.spinner); got != tc.want {
				t.Fatalf("want=%q got=%q", tc.want, got)
			}
			if (c.activeCache != nil) != (tc.current != "" && tc.spinner != "") {
				t.Fatal("active cache cleanup changed")
			}
			if c.current != tc.current {
				t.Fatal("assembly mutated streaming source")
			}
		})
	}
}

func TestStreamAssemblyKeepsLargePrefixAndCopies(t *testing.T) {
	prefix := strings.Repeat("archived line\n", 50000)
	original := newChat(80, "en")
	original.legacySymbols = false
	original.msgs = []msg{{role: roleSystem, text: "archived"}}
	original.completedCache = prefix
	original.current = "Answer."
	copyChat := original
	want := prefix + "\nSuperCli\n▌ Answer. spin\n"
	if got := copyChat.renderWithSpinner(NoColorPalette(), "spin"); got != want {
		t.Fatal("large prefix or answer bytes changed")
	}
	if original.completedCache != prefix || original.current != "Answer." || original.activeCache != nil {
		t.Fatal("rendering copied chat changed original cache/source")
	}
	copyChat.flushCurrent()
	if original.current != "Answer." || original.completedCache != prefix {
		t.Fatal("flushing copied stream changed original")
	}
}

func TestStreamAssemblyPreservesCallbackAndBranchOrder(t *testing.T) {
	c := newChat(80, "en")
	c.legacySymbols = false
	c.addUser("Prompt")
	c.current = "initial"
	p := NoColorPalette()
	var calls []string
	record := func(name string) func(string) string {
		return func(s string) string { calls = append(calls, name); return s }
	}
	p.UserLabel = p.UserLabel.Transform(func(s string) string { calls = append(calls, "completed label"); c.current = "replacement"; return s })
	p.User = p.User.Transform(record("completed body"))
	p.UserGutter = p.UserGutter.Transform(record("completed gutter"))
	p.AssistantLabel = p.AssistantLabel.Transform(func(s string) string { calls = append(calls, "active label"); c.msgs = nil; return s })
	p.Assistant = p.Assistant.Transform(record("active body"))
	p.AssistGutter = p.AssistGutter.Transform(record("active gutter"))
	want := "You\n▌ Prompt\n\nSuperCli\n▌ replacement spin\n"
	if got := c.renderWithSpinner(p, "spin"); got != want {
		t.Fatalf("callback mutation changed assembly boundary: want=%q got=%q", want, got)
	}
	order := []string{"completed label", "completed body", "completed gutter", "active label", "active body", "active gutter"}
	if !reflect.DeepEqual(calls, order) {
		t.Fatalf("callback order/count changed: %v", calls)
	}
}
