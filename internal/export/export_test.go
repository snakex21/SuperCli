package export

import (
	"strings"
	"testing"
	"time"

	"supercli/internal/session"
)

func TestRenderMarkdown_Basic(t *testing.T) {
	opts := Options{
		ID:        "abcdef123456",
		Title:     "Fix the bug",
		Model:     "gpt-4o",
		Cwd:       "/home/user/project",
		CreatedAt: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC),
		TokensIn:  1500,
		TokensOut: 500,
		Messages: []session.Encoded{
			{Role: "user", Content: "Fix the bug in main.go"},
			{Role: "assistant", Content: "I'll look at main.go first."},
		},
	}
	out := RenderMarkdown(opts)
	if !strings.Contains(out, "# SuperCli Session") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "gpt-4o") {
		t.Error("missing model")
	}
	if !strings.Contains(out, "Fix the bug") {
		t.Error("missing title")
	}
	if !strings.Contains(out, "## User") {
		t.Error("missing user section")
	}
	if !strings.Contains(out, "## Assistant") {
		t.Error("missing assistant section")
	}
	if !strings.Contains(out, "1500 / 500") {
		t.Error("missing token counts")
	}
}

func TestRenderMarkdown_ToolMessages(t *testing.T) {
	opts := Options{
		ID:    "test1234",
		Model: "echo",
		Messages: []session.Encoded{
			{Role: "tool", Name: "file_read", Content: "contents of file"},
			{Role: "tool", Name: "", Content: "generic tool output"},
		},
	}
	out := RenderMarkdown(opts)
	if !strings.Contains(out, "### file_read result") {
		t.Error("missing tool name in header")
	}
	if !strings.Contains(out, "contents of file") {
		t.Error("missing tool content")
	}
	if !strings.Contains(out, "### tool result") {
		t.Error("missing generic tool header")
	}
}

func TestRenderMarkdown_SystemMessages(t *testing.T) {
	opts := Options{
		ID:    "test1234",
		Model: "echo",
		Messages: []session.Encoded{
			{Role: "system", Content: "You are a helpful assistant"},
		},
	}
	out := RenderMarkdown(opts)
	if !strings.Contains(out, "## System") {
		t.Error("missing system section")
	}
	if !strings.Contains(out, "> You are a helpful assistant") {
		t.Error("missing quoted system content")
	}
}

func TestRenderMarkdown_EmptyContent(t *testing.T) {
	opts := Options{
		ID:    "test1234",
		Model: "echo",
		Messages: []session.Encoded{
			{Role: "user", Content: ""},
			{Role: "assistant", Content: "response"},
		},
	}
	out := RenderMarkdown(opts)
	// Empty messages should be skipped.
	if strings.Count(out, "## User") > 0 {
		t.Error("empty user message should be skipped")
	}
}

func TestDefaultFilename_WithTitle(t *testing.T) {
	opts := Options{ID: "abcdef123456", Title: "Fix the bug"}
	fn := DefaultFilename(opts)
	if !strings.HasPrefix(fn, "supercli-fix-the-bug-") {
		t.Errorf("DefaultFilename = %q", fn)
	}
	if !strings.HasSuffix(fn, ".md") {
		t.Errorf("DefaultFilename missing .md: %q", fn)
	}
}

func TestDefaultFilename_NoTitle(t *testing.T) {
	opts := Options{ID: "abcdef123456"}
	fn := DefaultFilename(opts)
	if !strings.HasPrefix(fn, "supercli-abcdef12") {
		t.Errorf("DefaultFilename = %q", fn)
	}
	if !strings.HasSuffix(fn, ".md") {
		t.Errorf("DefaultFilename missing .md: %q", fn)
	}
}

func TestDefaultFilename_Slugify(t *testing.T) {
	opts := Options{ID: "abc", Title: "Hello World / Test 123!"}
	fn := DefaultFilename(opts)
	if strings.Contains(fn, "/") {
		t.Errorf("slug should not contain /: %q", fn)
	}
	if strings.Contains(fn, "!") {
		t.Errorf("slug should not contain !: %q", fn)
	}
}
