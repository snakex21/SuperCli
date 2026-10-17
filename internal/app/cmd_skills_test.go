package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools"
	"supercli/internal/ui/tui"
)

func writeCommandSkill(t *testing.T, root, name, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nPRIVATE_SKILL_BODY_MUST_NOT_APPEAR\n", name, description)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsCommandListsAndFiltersMetadata(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()
	writeCommandSkill(t, project, "project-review", "Project review helper")
	writeCommandSkill(t, user, "project-review", "Shadowed user guidance")
	writeCommandSkill(t, user, "image-painter", "Create images")
	discoverer := &tools.Discoverer{Sources: []tools.Source{{Dir: project, Priority: 100}, {Dir: user, Priority: 45}}}
	command := skillsCommand(discoverer, "en")
	out, err := command(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Skills", "2/2", "project-review", "Project review helper", "image-painter", "Create images", "Project", "User", "/skills <query>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "PRIVATE_SKILL_BODY") || strings.Contains(out, "Shadowed user guidance") {
		t.Fatal("list exposed skill instructions or a shadowed source")
	}
	out, err = command(context.Background(), "   IMAGE   ")
	if err != nil || !strings.Contains(out, "image-painter") || strings.Contains(out, "project-review") || !strings.Contains(out, "1/1") {
		t.Fatalf("filter failed: %q, %v", out, err)
	}
	out, err = command(context.Background(), "not-present-anywhere")
	if err != nil || !strings.Contains(out, "No skills found.") || !strings.Contains(out, "0/0") {
		t.Fatalf("empty result failed: %q, %v", out, err)
	}
	out, err = skillsCommand(discoverer, "pl")(context.Background(), "review")
	if err != nil || !strings.Contains(out, "Umiejętności") || !strings.Contains(out, "Projekt") {
		t.Fatalf("localized list failed: %q, %v", out, err)
	}
}

func TestSkillsCommandBoundsListAndReportsErrors(t *testing.T) {
	t.Run("bounded metadata", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < 51; i++ {
			writeCommandSkill(t, root, fmt.Sprintf("fixture-%02d", i), "Small metadata entry")
		}
		command := skillsCommand(&tools.Discoverer{Sources: []tools.Source{{Dir: root, Priority: 100}}}, "en")
		out, err := command(context.Background(), "")
		if err != nil || !strings.Contains(out, "50/51") || strings.Count(out, "\n- **") != 50 {
			t.Fatalf("unbounded or incomplete list: %q, %v", out, err)
		}
	})
	t.Run("catalog error", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "broken", "SKILL.md"), 0o755); err != nil {
			t.Fatal(err)
		}
		command := skillsCommand(&tools.Discoverer{Sources: []tools.Source{{Dir: root, Priority: 100}}}, "en")
		out, err := command(context.Background(), "")
		if err == nil || out != "" || !strings.HasPrefix(err.Error(), "skills:") {
			t.Fatalf("missing catalog error: %q, %v", out, err)
		}
	})
}

func TestSkillsCommandWiredWithBuiltinsWithoutMaterializing(t *testing.T) {
	home, dataDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	commands := map[string]tui.SlashHandler{}
	wireSlashEarly(commands, slashWireDeps{home: home, cwd: home, dataDir: dataDir, uiLanguage: "en"})
	command := commands["skills"]
	if command == nil {
		t.Fatal("/skills was not registered")
	}
	builtins, _, err := tools.NewDiscovererWithBuiltins(home, dataDir).List("", 0, 1)
	if err != nil || len(builtins) == 0 {
		t.Fatalf("builtin catalog unavailable: %v", err)
	}
	out, err := command(context.Background(), builtins[0].Name)
	if err != nil || !strings.Contains(out, builtins[0].Name) {
		t.Fatalf("builtin metadata unavailable: %q, %v", out, err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("listing materialized builtin files: %v, %v", entries, err)
	}
}
