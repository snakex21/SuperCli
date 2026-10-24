package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestSlashCopyUsesLastAssistantAndReportsPath(t *testing.T) {
	oldNative, oldOSC := copyNativeClipboard, copyOSC52Clipboard
	t.Cleanup(func() { copyNativeClipboard, copyOSC52Clipboard = oldNative, oldOSC })
	var native, osc []string
	nativeErr := error(nil)
	copyNativeClipboard = func(s string) error { native = append(native, s); return nativeErr }
	copyOSC52Clipboard = func(s string) error { osc = append(osc, s); return nil }

	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: "en", NoColor: true})
	m.chat.addUser("question")
	m.chat.addAssistant("first reply")
	m.chat.addUser("again")
	m.chat.addAssistant("last reply")
	m.chat.addSystem("system line")

	next, _ := m.dispatchSlashCommand(SlashCommand{Name: "copy"})
	got := next.(Model)
	if len(native) != 1 || native[0] != "last reply" || len(osc) != 0 {
		t.Fatalf("native=%v osc=%v", native, osc)
	}
	if got.statusOverride != textFor("en", "tui.copy.native") {
		t.Fatalf("status=%q", got.statusOverride)
	}

	nativeErr = errors.New("no clipboard")
	next, _ = m.dispatchSlashCommand(SlashCommand{Name: "copy"})
	got = next.(Model)
	if len(osc) != 1 || osc[0] != "last reply" || got.statusOverride != textFor("en", "tui.copy.osc52") {
		t.Fatalf("osc fallback: osc=%v status=%q", osc, got.statusOverride)
	}

	empty := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: "en", NoColor: true})
	next, _ = empty.dispatchSlashCommand(SlashCommand{Name: "copy"})
	if s := next.(Model).statusOverride; s != textFor("en", "tui.model_input.afd6f64e92") {
		t.Fatalf("empty status=%q", s)
	}
	if !slashAvailableDuringRun(SlashCommand{Name: "copy"}) {
		t.Fatal("/copy must be safe during a run")
	}
	for _, language := range []string{"en", "pl"} {
		if !strings.Contains(HelpContentFor(language), "/copy") {
			t.Fatalf("/copy missing from %s help", language)
		}
	}
	if items := filterItems(buildSlashItems(nil), "cop"); len(items) == 0 || items[0].Label != "/copy" {
		t.Fatalf("missing /copy completion: %+v", items)
	}
}

func TestAssistantEditSettingsRowsTranslated(t *testing.T) {
	want := map[string]string{"assistant_edit": "Edycja odpowiedzi AI", "assistant_edit_model_sees": "Model widzi edytowane odpowiedzi"}
	for _, row := range settingsRowsFor("pl") {
		if label, ok := want[row.key]; ok {
			if row.label != label || !strings.Contains(row.desc, "") {
				t.Fatalf("%s label=%q", row.key, row.label)
			}
			delete(want, row.key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing rows: %v", want)
	}
	for _, row := range settingsRowsFor("en") {
		if row.key == "assistant_edit_model_sees" && !strings.Contains(row.desc, "prompt cache") {
			t.Fatalf("model-sees description must mention the prompt cache: %q", row.desc)
		}
	}
}
