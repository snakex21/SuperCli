package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/ui/tui"
)

func TestReasoningHistoryCommandPersistsAndValidates(t *testing.T) {
	old := llm.DiscardPreviousReasoning()
	t.Cleanup(func() { llm.SetDiscardPreviousReasoning(old) })
	dir := t.TempDir()
	commands := map[string]tui.SlashHandler{}
	wireSlashEarly(commands, slashWireDeps{dataDir: dir, cwd: dir})
	command := commands["reasoning-history"]
	if command == nil {
		t.Fatal("command not registered")
	}
	path, _ := config.FindTomlPaths(dir, dir)
	if err := config.SaveToml(path, config.TomlConfig{DefaultModel: "preserve-this-model"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"drop", "keep", "default"} {
		out, err := command(context.Background(), mode)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadToml(path)
		if err != nil {
			t.Fatal(err)
		}
		if llm.DiscardPreviousReasoning() != (mode == "drop") || cfg.DefaultModel != "preserve-this-model" {
			t.Fatalf("mode=%s output=%s cfg=%+v", mode, out, cfg)
		}
		if (cfg.DiscardPreviousReasoning == nil) != (mode == "default") {
			t.Fatal("default did not clear override")
		}
	}
	before, _ := os.ReadFile(path)
	if out, err := command(context.Background(), "nonsense"); err != nil || !strings.Contains(out, "usage") {
		t.Fatal(out, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid option wrote config")
	}
	// A failed load must not wipe the file or change the running preference.
	llm.SetDiscardPreviousReasoning(true)
	if err := os.WriteFile(filepath.Clean(path), []byte("invalid = ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := command(context.Background(), "keep"); err == nil || !llm.DiscardPreviousReasoning() {
		t.Fatal("load failure changed runtime")
	}
}
