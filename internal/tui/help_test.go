package tui

import (
	"strings"
	"testing"
)

// RenderHelp is a thin re-export; verify it delegates correctly.
func TestRenderHelp_DelegatesToHelpContent(t *testing.T) {
	rh := RenderHelp()
	hc := HelpContent()
	if rh != hc {
		t.Fatalf("RenderHelp != HelpContent")
	}
}

// The old box-drawing format is gone; verify no box chars remain.
func TestFormatHelp_NoBoxDrawing(t *testing.T) {
	rendered := FormatHelp(nil)
	if strings.Contains(rendered, "╔") || strings.Contains(rendered, "╚") {
		t.Fatalf("FormatHelp should not contain box-drawing characters: %q", rendered)
	}
}

// Verify the HelpContent covers all core slash commands.
func TestHelpContent_AllCoreCommands(t *testing.T) {
	h := HelpContent()
	cmds := []string{
		"/help", "/goal", "/darwin", "/council", "/clear",
		"/reflect", "/compact", "/status", "/models", "/sandbox",
		"/plan", "/diff", "/model", "/resume", "/export", "/cost", "/undo", "/providers", "/quit",
	}
	for _, c := range cmds {
		if !strings.Contains(h, c) {
			t.Errorf("HelpContent missing %s", c)
		}
	}
}

// RenderHelp should include key hints.
func TestRenderHelp_KeysSection(t *testing.T) {
	rh := RenderHelp()
	if !strings.Contains(rh, "PgUp") {
		t.Fatal("RenderHelp missing PgUp hint")
	}
	if !strings.Contains(rh, "Ctrl+C") {
		t.Fatal("RenderHelp missing Ctrl+C hint")
	}
}
