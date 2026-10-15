package windowautomation

import (
	"strings"
	"testing"
)

func TestValidateRejectsUnscopedAndPhysicalInputRequests(t *testing.T) {
	for _, r := range []Request{
		{Action: "inspect_window"},
		{Identity: Identity{PID: 1}, Action: "mouse"},
		{Identity: Identity{PID: 1}, Action: "click"},
		{Identity: Identity{PID: 1}, Action: "type"},
		{Identity: Identity{PID: 1}, Action: "inspect_window", WindowTitle: "a", WindowID: "1"},
		{Identity: Identity{PID: 1}, Action: "type", ElementID: "el_x", Text: "a\x00b"},
		{Identity: Identity{PID: 1}, Action: "type", ElementID: "el_x", Text: strings.Repeat("a", 16385)},
	} {
		if err := Validate(r); err == nil {
			t.Fatalf("unsafe request accepted: %+v", r)
		}
	}
	if err := Validate(Request{Identity: Identity{PID: 1}, Action: "type", ElementID: "el_x", Text: "zażółć"}); err != nil {
		t.Fatal(err)
	}
}

func TestPickWindowPrefersModalDialogAndIgnoresMenus(t *testing.T) {
	main := WindowCandidate{HWND: 0x10, Title: "Bez tytułu - Notatnik", Visible: true}
	dialog := WindowCandidate{HWND: 0x20, Title: "Notatnik", Visible: true, Enabled: true, Owner: 0x10}
	menu := WindowCandidate{HWND: 0x30, Visible: true, Enabled: true, Menu: true}
	tool := WindowCandidate{HWND: 0x40, Title: "IME", Visible: true, Enabled: true, Auxiliary: true}
	enabledMain := main
	enabledMain.Enabled = true
	unownedDialog := dialog
	unownedDialog.Owner = 0
	for name, tc := range map[string]struct {
		candidates []WindowCandidate
		want       int
	}{
		"modal over disabled owner":   {[]WindowCandidate{main, dialog}, 1},
		"main window with open menu":  {[]WindowCandidate{enabledMain, menu}, 0},
		"modal with menu and tool":    {[]WindowCandidate{menu, main, tool, dialog}, 3},
		"owned enabled dialog":        {[]WindowCandidate{enabledMain, dialog}, 1},
		"only menus":                  {[]WindowCandidate{menu}, -1},
		"two unrelated enabled":       {[]WindowCandidate{enabledMain, unownedDialog}, -1},
		"hidden window loses":         {[]WindowCandidate{{HWND: 1, Title: "a", Enabled: true}, {HWND: 2, Title: "b", Enabled: true, Visible: true}}, 1},
		"untitled helper loses":       {[]WindowCandidate{{HWND: 1, Visible: true, Enabled: true}, {HWND: 2, Title: "Game", Visible: true, Enabled: true}}, 1},
		"single auxiliary still used": {[]WindowCandidate{tool}, 0},
	} {
		if got := PickWindow(tc.candidates); got != tc.want {
			t.Errorf("%s: PickWindow=%d want %d", name, got, tc.want)
		}
	}
}

func TestAmbiguousWindowErrorListsBoundedChoices(t *testing.T) {
	var candidates []WindowCandidate
	for i := 1; i <= 12; i++ {
		candidates = append(candidates, WindowCandidate{HWND: uintptr(0xA0 + i), Title: "Game " + strings.Repeat("x", 200), Visible: true, Enabled: true})
	}
	candidates = append(candidates, WindowCandidate{HWND: 0xFF, Visible: true, Menu: true})
	err := ambiguousWindowError(candidates).Error()
	if !strings.Contains(err, "12 matching windows") || !strings.Contains(err, `0xA1 "Game x`) || !strings.Contains(err, "0xA8 ") ||
		strings.Contains(err, "0xA9 ") || strings.Contains(err, "0xFF") || !strings.Contains(err, "4 more") || len(err) > 1400 {
		t.Fatalf("unexpected ambiguity error: %s", err)
	}
}
