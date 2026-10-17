package tui

import (
	"context"
	"strings"
	"testing"
)

func TestSkillsCommandIsDiscoverable(t *testing.T) {
	items := filterItems(buildSlashItems(nil), "ski")
	if len(items) != 1 || items[0].Label != "/skills" || items[0].Value != "/skills " || items[0].Desc == "" {
		t.Fatalf("missing /skills completion: %+v", items)
	}
	for _, language := range []string{"en", "pl"} {
		for _, help := range []string{HelpContentFor(language), HelpContentAllFor(language)} {
			if !strings.Contains(help, "/skills") || !strings.Contains(help, textFor(language, "tui.command_desc.skills")) {
				t.Fatalf("missing localized /skills help for %s", language)
			}
		}
	}
	if !localSlashCommands["skills"] || !slashAvailableDuringRun(SlashCommand{Name: "skills", Args: "review"}) {
		t.Fatal("metadata browsing must remain local and safe during a run")
	}
}

func TestSkillsCommandStaysLocalDuringActiveRun(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "busy"}[busy], func(t *testing.T) {
			m := New(Options{Home: t.TempDir(), NoColor: true})
			m.busy = busy
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.cancel.Arm(cancelRun, cancel)
			m.commands = map[string]SlashHandler{
				"skills": func(_ context.Context, args string) (string, error) {
					if args != "review" {
						t.Fatalf("lost query: %q", args)
					}
					return "# Skills\n\nmetadata only", nil
				},
			}
			next, run := m.dispatchSlashCommand(SlashCommand{Name: "skills", Args: "review"})
			m = next.(Model)
			if run == nil || m.busy != busy || ctx.Err() != nil {
				t.Fatal("/skills changed the active run before reading metadata")
			}
			result, ok := run().(slashResultMsg)
			if !ok || !result.Local || !result.Document || result.Err != nil || !strings.Contains(result.Body, "metadata only") {
				t.Fatalf("unexpected local result: %+v", result)
			}
			next, _ = m.Update(result)
			m = next.(Model)
			if m.busy != busy || ctx.Err() != nil {
				t.Fatal("showing /skills stopped or replaced the active run")
			}
		})
	}
}
