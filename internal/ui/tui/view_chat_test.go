package tui

import (
	"strings"
	"testing"
)

func TestChat_AddUser(t *testing.T) {
	c := newChat(80)
	c.addUser("hello")
	if len(c.msgs) != 1 {
		t.Fatalf("msgs = %d, want 1", len(c.msgs))
	}
	if c.msgs[0].role != roleUser {
		t.Fatal("role should be roleUser")
	}
	if c.msgs[0].text != "hello" {
		t.Fatalf("text = %q", c.msgs[0].text)
	}
}

func TestChat_AddSystem(t *testing.T) {
	c := newChat(80)
	c.addSystem("[draft: saved 5 tokens]")
	if c.msgs[0].role != roleSystem {
		t.Fatal("role should be roleSystem")
	}
}

func TestChat_AddAssistant(t *testing.T) {
	c := newChat(80)
	c.addAssistant("I can help with that.")
	if c.msgs[0].role != roleAssistant {
		t.Fatal("role should be roleAssistant")
	}
}

func TestChat_AppendCurrent(t *testing.T) {
	c := newChat(80)
	c.appendCurrent("hello ")
	c.appendCurrent("world")
	if c.current != "hello world" {
		t.Fatalf("current = %q", c.current)
	}
}

func TestChat_FlushCurrent(t *testing.T) {
	c := newChat(80)
	c.appendCurrent("streaming text")
	c.flushCurrent()
	if c.current != "" {
		t.Fatal("current should be empty after flush")
	}
	if len(c.msgs) != 1 {
		t.Fatalf("msgs = %d, want 1", len(c.msgs))
	}
	if c.msgs[0].role != roleAssistant {
		t.Fatal("flushed message should be roleAssistant")
	}
	if c.msgs[0].text != "streaming text" {
		t.Fatalf("text = %q", c.msgs[0].text)
	}
}

func TestChat_FlushCurrent_Empty(t *testing.T) {
	c := newChat(80)
	c.flushCurrent() // no-op
	if len(c.msgs) != 0 {
		t.Fatal("no messages should be added when current is empty")
	}
}

func TestChat_Render_WithMessages(t *testing.T) {
	p := DefaultPalette()
	c := newChat(80)
	c.addUser("hi")
	c.addAssistant("hello!")
	rendered := c.render(p)
	if !strings.Contains(rendered, "You") {
		t.Fatalf("rendered missing 'You' prefix: %q", rendered)
	}
	if !strings.Contains(rendered, "hello!") {
		t.Fatalf("rendered missing assistant text: %q", rendered)
	}
}

func TestChat_Render_WithCurrent(t *testing.T) {
	p := DefaultPalette()
	c := newChat(80)
	c.appendCurrent("streaming...")
	rendered := c.render(p)
	if !strings.Contains(rendered, "streaming...") {
		t.Fatalf("rendered missing current text: %q", rendered)
	}
}

func TestChat_RenderWithSpinner(t *testing.T) {
	p := DefaultPalette()
	c := newChat(80)
	c.appendCurrent("thinking")
	rendered := c.renderWithSpinner(p, "(spinner)")
	if !strings.Contains(rendered, "thinking") {
		t.Fatalf("missing current text: %q", rendered)
	}
	if !strings.Contains(rendered, "(spinner)") {
		t.Fatalf("missing spinner: %q", rendered)
	}
}

func TestChat_RenderWithSpinner_NoCurrent(t *testing.T) {
	p := DefaultPalette()
	c := newChat(80)
	rendered := c.renderWithSpinner(p, "(spinner)")
	// Spinner should still appear even without current text.
	if !strings.Contains(rendered, "(spinner)") {
		t.Fatalf("spinner should show even without current: %q", rendered)
	}
}

func TestChat_LastRole_Empty(t *testing.T) {
	c := newChat(80)
	if c.lastRole() != roleSystem {
		t.Fatal("empty chat should default to roleSystem")
	}
}

func TestChat_LastRole_AfterUser(t *testing.T) {
	c := newChat(80)
	c.addUser("test")
	if c.lastRole() != roleUser {
		t.Fatal("last role should be roleUser")
	}
}

func TestChat_Len(t *testing.T) {
	c := newChat(80)
	if c.len() != 0 {
		t.Fatal("empty chat len should be 0")
	}
	c.addUser("a")
	c.addSystem("b")
	if c.len() != 2 {
		t.Fatalf("len = %d, want 2", c.len())
	}
}

func TestChat_Render_MultipleMessages(t *testing.T) {
	p := DefaultPalette()
	c := newChat(80)
	c.addUser("q1")
	c.addAssistant("a1")
	c.addUser("q2")
	c.addAssistant("a2")
	rendered := c.render(p)
	// Should contain both user prefixes.
	count := strings.Count(rendered, "You")
	if count != 2 {
		t.Fatalf("expected 2 'You' prefixes, got %d", count)
	}
}

func TestChat_RenderAddsTurnSpacingAndHidesStoredPromptMarker(t *testing.T) {
	p := NoColorPalette()
	c := newChat(80)
	c.addUser("> inspect this")
	c.addSystem("tool event")
	c.addAssistant("done")
	rendered := c.render(p)
	if strings.Contains(rendered, "▌ > inspect this") {
		t.Fatalf("stored prompt marker leaked into styled chat: %q", rendered)
	}
	if !strings.Contains(rendered, "You\n▌ inspect this") {
		t.Fatalf("user block not rendered cleanly: %q", rendered)
	}
	if !strings.Contains(rendered, "tool event\n\nSuperCli") {
		t.Fatalf("assistant boundary needs breathing room: %q", rendered)
	}
}

func TestChatCompletedRenderCacheSurvivesStreamingDeltas(t *testing.T) {
	p := NoColorPalette()
	c := newChat(80)
	c.addAssistant("completed **answer**")
	first := c.renderCompleted(p)
	if c.completedDirty || first == "" {
		t.Fatalf("completed cache not populated: dirty=%v value=%q", c.completedDirty, first)
	}
	c.appendCurrent("one delta")
	second := c.renderCompleted(p)
	if second != first || c.completedDirty {
		t.Fatalf("streaming invalidated immutable prefix: first=%q second=%q dirty=%v", first, second, c.completedDirty)
	}
	c.addSystem("new event")
	if !c.completedDirty {
		t.Fatal("completed message must invalidate the cache")
	}
}

func TestChatSearchAndIndividualFold(t *testing.T) {
	p := NoColorPalette()
	c := newChat(80)
	c.addUser("find the config")
	c.addSystem("read_lines done\nline one\nline two")
	c.addAssistant("config is ready")
	matches := c.search("config")
	if len(matches) != 2 || matches[0].MessageIndex != 0 || matches[1].MessageIndex != 2 {
		t.Fatalf("matches=%+v", matches)
	}
	if !c.toggleMessage(1) {
		t.Fatal("multi-line tool block should collapse")
	}
	rendered := c.render(p)
	if strings.Contains(rendered, "line two") || !strings.Contains(rendered, "collapsed") {
		t.Fatalf("folded block rendered incorrectly: %q", rendered)
	}
}
