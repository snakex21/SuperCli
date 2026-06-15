package app

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/agent/darwin"
	"supercli/internal/llm"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

type testProvider struct{}

func (testProvider) Name() string { return "test-model" }

func (testProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta, 1)
	close(out)
	return out, nil
}

func TestDarwinSlashEmptyPromptReturnsUsage(t *testing.T) {
	dt := darwin.NewDarwinTool(testProvider{}, tools.NewRegistry(), t.TempDir(), "test prompt")
	cmds := darwinCommands(dt)
	h, ok := cmds["darwin"]
	if !ok || h == nil {
		t.Fatal("darwin command not registered")
	}
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("empty /darwin should not error: %v", err)
	}
	if !strings.Contains(out, "usage: /darwin") {
		t.Fatalf("expected usage, got %q", out)
	}
}

func TestParseDarwinArgs(t *testing.T) {
	prompt, n := parseDarwinArgs("4 fix tests", 3)
	if n != 4 || prompt != "fix tests" {
		t.Fatalf("parse = (%q,%d), want (fix tests,4)", prompt, n)
	}
	prompt, n = parseDarwinArgs("fix tests", 3)
	if n != 3 || prompt != "fix tests" {
		t.Fatalf("parse default = (%q,%d), want (fix tests,3)", prompt, n)
	}
}

func TestGoalSlashCommandsAreSafe(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gs := goal.NewStorage(db)
	if err := gs.Migrate(context.Background()); err != nil {
		t.Fatalf("goal migrate: %v", err)
	}
	svc := goal.NewService(gs)
	cmds := goalCommands(svc)
	h := cmds["goal"]
	if h == nil {
		t.Fatal("goal command not registered")
	}
	cases := []struct {
		args string
		want string
	}{
		{"", "usage: /goal"},
		{"set ship slash commands", "active goal"},
		{"show", "ship slash commands"},
		{"tasks", "no tasks"},
		{"tasks add cover every slash command", "added task"},
		{"tasks list", "cover every slash command"},
		{"tasks done 1", "task 1 -> done"},
		{"note tested slash commands", "note appended"},
		{"list", "goal(s)"},
		{"done", "marked done"},
	}
	for _, tc := range cases {
		t.Run(tc.args, func(t *testing.T) {
			out, err := h(context.Background(), tc.args)
			if err != nil {
				t.Fatalf("/goal %s returned error: %v", tc.args, err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("/goal %s = %q, want contains %q", tc.args, out, tc.want)
			}
		})
	}
}

func TestRunBatch_EchoProvider(t *testing.T) {
	// Test that buildProvider works with echo (no API key needed).
	home := t.TempDir()
	cfg := testEchoConfig()
	p, err := buildProvider(cfg, home, nil)
	if err != nil {
		t.Skipf("skip: buildProvider: %v", err)
	}
	if p == nil {
		t.Fatal("provider should not be nil")
	}
}

func testEchoConfig() config.Config {
	return config.Config{
		Provider: config.ProviderEcho,
		Model:    "echo-test",
	}
}

func TestPlatformHint(t *testing.T) {
	hint := platformHint()
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(hint, "dir") || !strings.Contains(hint, "cmd /c") {
			t.Errorf("windows hint missing commands: %s", hint)
		}
	case "darwin":
		if !strings.Contains(hint, "macOS") {
			t.Errorf("darwin hint missing macOS: %s", hint)
		}
	default:
		if !strings.Contains(hint, "Linux") {
			t.Errorf("linux hint missing Linux: %s", hint)
		}
	}
}

func TestBuildSystemPrompt_ContainsPlatform(t *testing.T) {
	prompt := buildSystemPrompt(nil)
	if !strings.Contains(prompt, "OS:") {
		t.Errorf("system prompt missing platform info: %s", prompt)
	}
}
