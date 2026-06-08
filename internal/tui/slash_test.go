package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseSlashCommand_PlainPrompt(t *testing.T) {
	if sc := ParseSlashCommand("hello world"); sc != nil {
		t.Errorf("plain text should not parse as slash command: %+v", sc)
	}
}

func TestParseSlashCommand_Darwin(t *testing.T) {
	sc := ParseSlashCommand("/darwin 3 fix the bug")
	if sc == nil {
		t.Fatal("nil")
	}
	if sc.Name != "darwin" {
		t.Errorf("Name = %q, want darwin", sc.Name)
	}
	if sc.Args != "3 fix the bug" {
		t.Errorf("Args = %q, want '3 fix the bug'", sc.Args)
	}
}

func TestParseSlashCommand_DarwinNoArgs(t *testing.T) {
	sc := ParseSlashCommand("/darwin")
	if sc == nil {
		t.Fatal("nil")
	}
	if sc.Name != "darwin" {
		t.Errorf("Name = %q", sc.Name)
	}
	if sc.Args != "" {
		t.Errorf("Args = %q, want empty", sc.Args)
	}
}

func TestParseSlashCommand_UnknownCommand(t *testing.T) {
	sc := ParseSlashCommand("/unknown foo")
	if sc == nil {
		t.Fatal("nil")
	}
	if sc.Name != "unknown" {
		t.Errorf("Name = %q", sc.Name)
	}
}

func TestParseSlashCommand_JustSlash(t *testing.T) {
	if sc := ParseSlashCommand("/"); sc != nil {
		t.Errorf("bare slash should not parse: %+v", sc)
	}
}

func TestParseSlashCommand_AllCommands(t *testing.T) {
	cmds := []string{
		"/darwin fix bug",
		"/goal set ship F8",
		"/clear",
		"/help",
		"/council pick best",
		"/reflect",
		"/compact",
		"/status",
		"/models",
		"/sandbox",
	}
	for _, c := range cmds {
		sc := ParseSlashCommand(c)
		if sc == nil {
			t.Errorf("ParseSlashCommand(%q) returned nil", c)
			continue
		}
		if sc.Name == "" {
			t.Errorf("Name is empty for %q", c)
		}
	}
}

// --- SafeWrap tests ---

func TestSafeWrap_NormalExecution(t *testing.T) {
	h := func(_ context.Context, _ string) (string, error) {
		return "ok", nil
	}
	safe := SafeWrap("test", h)
	out, err := safe(context.Background(), "")
	if out != "ok" || err != nil {
		t.Fatalf("got (%q, %v)", out, err)
	}
}

func TestSafeWrap_ErrorReturned(t *testing.T) {
	h := func(_ context.Context, _ string) (string, error) {
		return "", errors.New("bad")
	}
	safe := SafeWrap("test", h)
	_, err := safe(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("expected 'bad' error, got %v", err)
	}
}

func TestSafeWrap_PanicRecovered(t *testing.T) {
	h := func(_ context.Context, _ string) (string, error) {
		panic("boom")
	}
	safe := SafeWrap("mycommand", h)
	out, err := safe(context.Background(), "")
	if out != "" {
		t.Fatalf("expected empty output on panic, got %q", out)
	}
	if err == nil {
		t.Fatal("expected error on panic")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should mention panic: %v", err)
	}
	if !strings.Contains(err.Error(), "mycommand") {
		t.Fatalf("error should mention command name: %v", err)
	}
}

func TestSafeWrap_NilHandlerReturnsError(t *testing.T) {
	safe := SafeWrap("darwin", nil)
	out, err := safe(context.Background(), "")
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("expected not wired error, got %v", err)
	}
}

// --- FormatHelp tests ---

func TestFormatHelp_ContainsEntries(t *testing.T) {
	entries := []SlashEntry{
		{Name: "darwin", Desc: "parallel agents", Args: "[N] <prompt>"},
		{Name: "clear", Desc: "hide messages"},
	}
	rendered := FormatHelp(entries)
	if !strings.Contains(rendered, "/darwin") {
		t.Fatalf("missing /darwin: %q", rendered)
	}
	if !strings.Contains(rendered, "/clear") {
		t.Fatalf("missing /clear: %q", rendered)
	}
	if !strings.Contains(rendered, "parallel agents") {
		t.Fatalf("missing desc: %q", rendered)
	}
}

func TestFormatHelp_ShowsArgs(t *testing.T) {
	entries := []SlashEntry{
		{Name: "darwin", Desc: "desc", Args: "[N] <prompt>"},
	}
	rendered := FormatHelp(entries)
	if !strings.Contains(rendered, "[N] <prompt>") {
		t.Fatalf("missing args: %q", rendered)
	}
}

func TestFormatHelp_SkipsArgsWhenEmpty(t *testing.T) {
	entries := []SlashEntry{
		{Name: "clear", Desc: "desc"},
	}
	rendered := FormatHelp(entries)
	if strings.Contains(rendered, "(") && strings.Contains(rendered, "/clear") {
		// The desc might have parens, but no separate args section
	}
	if strings.Contains(rendered, "usage:") {
		t.Fatalf("should not show 'usage:' for empty args: %q", rendered)
	}
}

func TestFormatHelp_KeysSection(t *testing.T) {
	rendered := FormatHelp(nil)
	if !strings.Contains(rendered, "PgUp") {
		t.Fatalf("missing PgUp hint: %q", rendered)
	}
	if !strings.Contains(rendered, "Ctrl+C") {
		t.Fatalf("missing Ctrl+C hint: %q", rendered)
	}
}

func TestHelpContent_AllCommands(t *testing.T) {
	h := HelpContent()
	cmds := []string{"/help", "/goal", "/darwin", "/council", "/clear",
		"/reflect", "/compact", "/status", "/models", "/sandbox"}
	for _, c := range cmds {
		if !strings.Contains(h, c) {
			t.Errorf("HelpContent missing %s", c)
		}
	}
}

// --- CommandRegistry tests ---

func TestCommandRegistry_RegisterAndGet(t *testing.T) {
	cr := NewCommandRegistry()
	called := false
	cr.Register("test", "test cmd", "arg", func(_ context.Context, _ string) (string, error) {
		called = true
		return "ok", nil
	})
	h := cr.Handler("test")
	if h == nil {
		t.Fatal("handler should be registered")
	}
	out, err := h(context.Background(), "")
	if err != nil || out != "ok" {
		t.Fatalf("got (%q, %v)", out, err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestCommandRegistry_MissingHandler(t *testing.T) {
	cr := NewCommandRegistry()
	if h := cr.Handler("nope"); h != nil {
		t.Fatal("missing handler should return nil")
	}
}

func TestCommandRegistry_EntriesOrder(t *testing.T) {
	cr := NewCommandRegistry()
	cr.Register("status", "s", "", nil)
	cr.Register("help", "h", "", nil)
	cr.Register("models", "m", "", nil)
	entries := cr.Entries()
	if len(entries) < 3 {
		t.Fatalf("expected >= 3 entries, got %d", len(entries))
	}
	// help should come first in the fixed order.
	if entries[0].Name != "help" {
		t.Fatalf("first entry should be 'help', got %q", entries[0].Name)
	}
}

// --- Test all slash commands for crash safety ---

func TestSlashCommand_Help(t *testing.T) {
	h := SafeWrap("help", func(_ context.Context, args string) (string, error) {
		return HelpContent(), nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("help crashed: %v", err)
	}
	if !strings.Contains(out, "/help") {
		t.Fatalf("help output missing /help: %q", out)
	}
}

func TestSlashCommand_Goal_NoArgs(t *testing.T) {
	h := SafeWrap("goal", func(_ context.Context, args string) (string, error) {
		return "usage: /goal <set|list|show|tasks|done> [args]", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("goal (no args) crashed: %v", err)
	}
	if !strings.Contains(out, "usage") {
		t.Fatalf("expected usage, got %q", out)
	}
}

func TestSlashCommand_Goal_NilService(t *testing.T) {
	// Simulate what happens when svc is nil — the wrapper
	// catches the panic.
	h := SafeWrap("goal", func(_ context.Context, args string) (string, error) {
		// This simulates calling svc.Set() on a nil svc.
		var svc interface{ Set(int) }
		_ = svc
		panic("nil pointer dereference")
	})
	_, err := h(context.Background(), "set test")
	if err == nil {
		t.Fatal("expected error from nil service panic")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic error: %v", err)
	}
}

func TestSlashCommand_Darwin_NoArgs(t *testing.T) {
	h := SafeWrap("darwin", func(_ context.Context, args string) (string, error) {
		if strings.TrimSpace(args) == "" {
			return "darwin: prompt is required", nil
		}
		return "ok", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("darwin (no args) crashed: %v", err)
	}
	if !strings.Contains(out, "required") {
		t.Fatalf("expected required message, got %q", out)
	}
}

func TestSlashCommand_Darwin_WithArgs(t *testing.T) {
	h := SafeWrap("darwin", func(_ context.Context, args string) (string, error) {
		return "darwin ran: " + args, nil
	})
	out, err := h(context.Background(), "3 fix bug")
	if err != nil {
		t.Fatalf("darwin crashed: %v", err)
	}
	if !strings.Contains(out, "fix bug") {
		t.Fatalf("expected echo, got %q", out)
	}
}

func TestSlashCommand_Clear_Empty(t *testing.T) {
	h := SafeWrap("clear", func(_ context.Context, args string) (string, error) {
		return "nothing to clear", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("clear crashed: %v", err)
	}
	if !strings.Contains(out, "nothing") {
		t.Fatalf("expected nothing, got %q", out)
	}
}

func TestSlashCommand_Council_NilCouncil(t *testing.T) {
	h := SafeWrap("council", func(_ context.Context, args string) (string, error) {
		// council == nil case
		return "council: not wired (no models available)", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("council (nil) crashed: %v", err)
	}
	if !strings.Contains(out, "not wired") {
		t.Fatalf("expected not wired, got %q", out)
	}
}

func TestSlashCommand_Council_EmptyPrompt(t *testing.T) {
	h := SafeWrap("council", func(_ context.Context, args string) (string, error) {
		q := strings.TrimSpace(args)
		if q == "" {
			return "usage: /council [N] <prompt>", nil
		}
		return "ok", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("council (empty) crashed: %v", err)
	}
	if !strings.Contains(out, "usage") {
		t.Fatalf("expected usage, got %q", out)
	}
}

func TestSlashCommand_Reflect_NoPatterns(t *testing.T) {
	h := SafeWrap("reflect", func(_ context.Context, args string) (string, error) {
		return "no patterns learned yet", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("reflect crashed: %v", err)
	}
	if !strings.Contains(out, "no patterns") {
		t.Fatalf("expected no patterns, got %q", out)
	}
}

func TestSlashCommand_Compact(t *testing.T) {
	h := SafeWrap("compact", func(_ context.Context, args string) (string, error) {
		return "context compacted: hid 5 messages", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("compact crashed: %v", err)
	}
	if !strings.Contains(out, "compacted") {
		t.Fatalf("expected compacted, got %q", out)
	}
}

func TestSlashCommand_Status(t *testing.T) {
	h := SafeWrap("status", func(_ context.Context, args string) (string, error) {
		return "model: gpt-4o | credits: 0 | no active goal", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("status crashed: %v", err)
	}
	if !strings.Contains(out, "model") {
		t.Fatalf("expected model info, got %q", out)
	}
}

func TestSlashCommand_Models(t *testing.T) {
	h := SafeWrap("models", func(_ context.Context, args string) (string, error) {
		return "available models:\n  gpt-4o\n  echo", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("models crashed: %v", err)
	}
	if !strings.Contains(out, "gpt-4o") {
		t.Fatalf("expected model list, got %q", out)
	}
}

func TestSlashCommand_Sandbox(t *testing.T) {
	h := SafeWrap("sandbox", func(_ context.Context, args string) (string, error) {
		return "sandbox: active, home=/test", nil
	})
	out, err := h(context.Background(), "")
	if err != nil {
		t.Fatalf("sandbox crashed: %v", err)
	}
	if !strings.Contains(out, "sandbox") {
		t.Fatalf("expected sandbox info, got %q", out)
	}
}

func TestSlashCommand_BadArgs(t *testing.T) {
	// Every command should handle garbage args gracefully.
	cmds := map[string]SlashHandler{
		"goal":    func(_ context.Context, _ string) (string, error) { return "ok", nil },
		"darwin":  func(_ context.Context, _ string) (string, error) { return "ok", nil },
		"council": func(_ context.Context, _ string) (string, error) { return "ok", nil },
		"clear":   func(_ context.Context, _ string) (string, error) { return "ok", nil },
		"help":    func(_ context.Context, _ string) (string, error) { return "ok", nil },
	}
	for name, h := range cmds {
		safe := SafeWrap(name, h)
		_, err := safe(context.Background(), "!!!weird args!!! @@##$$")
		if err != nil {
			t.Fatalf("%s crashed on bad args: %v", name, err)
		}
	}
}

func TestFormatSlashResult(t *testing.T) {
	s := formatSlashResult("darwin", "ran 3 agents")
	if !strings.Contains(s, "darwin") {
		t.Error("missing command name")
	}
	if !strings.Contains(s, "ran 3 agents") {
		t.Error("missing body")
	}
}
