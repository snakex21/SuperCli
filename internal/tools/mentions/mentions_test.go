package mentions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_SingleMention(t *testing.T) {
	remaining, paths := Parse("@src/main.go refactor this")
	if remaining != "refactor this" {
		t.Errorf("remaining = %q, want 'refactor this'", remaining)
	}
	if len(paths) != 1 || paths[0] != "src/main.go" {
		t.Errorf("paths = %v, want [src/main.go]", paths)
	}
}

func TestParse_MultipleMentions(t *testing.T) {
	remaining, paths := Parse("@a.go @b.go merge them")
	if remaining != "merge them" {
		t.Errorf("remaining = %q, want 'merge them'", remaining)
	}
	if len(paths) != 2 {
		t.Fatalf("len(paths) = %d, want 2", len(paths))
	}
	if paths[0] != "a.go" || paths[1] != "b.go" {
		t.Errorf("paths = %v, want [a.go b.go]", paths)
	}
}

func TestParse_NoMentions(t *testing.T) {
	remaining, paths := Parse("just plain text")
	if remaining != "just plain text" {
		t.Errorf("remaining = %q", remaining)
	}
	if len(paths) != 0 {
		t.Errorf("paths = %v, want empty", paths)
	}
}

func TestParse_MentionAtStartOnly(t *testing.T) {
	// @ must be preceded by whitespace or be at start.
	remaining, paths := Parse("email@domain.com fix this")
	if remaining != "email@domain.com fix this" {
		t.Errorf("remaining = %q", remaining)
	}
	if len(paths) != 0 {
		t.Errorf("should not parse email-style @: %v", paths)
	}
}

func TestParse_CommaSeparated(t *testing.T) {
	remaining, paths := Parse("@a.go,@b.go,@c.go done")
	if remaining != "done" {
		t.Errorf("remaining = %q", remaining)
	}
	if len(paths) != 3 {
		t.Fatalf("len(paths) = %d, want 3", len(paths))
	}
}

func TestParse_JustAtSign(t *testing.T) {
	remaining, paths := Parse("@")
	if remaining != "@" {
		t.Errorf("remaining = %q", remaining)
	}
	if len(paths) != 0 {
		t.Errorf("paths = %v, want empty", paths)
	}
}

func TestParse_EmptyString(t *testing.T) {
	remaining, paths := Parse("")
	if remaining != "" {
		t.Errorf("remaining = %q", remaining)
	}
	if len(paths) != 0 {
		t.Errorf("paths = %v, want empty", paths)
	}
}

func TestParse_PathWithSlashes(t *testing.T) {
	remaining, paths := Parse("@internal/tui/model.go check this")
	if remaining != "check this" {
		t.Errorf("remaining = %q", remaining)
	}
	if len(paths) != 1 || paths[0] != "internal/tui/model.go" {
		t.Errorf("paths = %v", paths)
	}
}

func TestResolve_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	mentions := Resolve(dir, []string{"hello.txt"}, 0)
	if len(mentions) != 1 {
		t.Fatalf("len = %d", len(mentions))
	}
	if mentions[0].Content != "hello world" {
		t.Errorf("content = %q", mentions[0].Content)
	}
	if mentions[0].Tokens != 3 { // "hello world" = 11 chars → (11+3)/4 = 3
		t.Errorf("tokens = %d, want 3", mentions[0].Tokens)
	}
}

func TestResolve_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "abs.txt")
	if err := os.WriteFile(f, []byte("absolute"), 0o644); err != nil {
		t.Fatal(err)
	}
	mentions := Resolve("/nowhere", []string{f}, 0)
	if len(mentions) != 1 {
		t.Fatalf("len = %d", len(mentions))
	}
	if mentions[0].Content != "absolute" {
		t.Errorf("content = %q", mentions[0].Content)
	}
}

func TestResolve_TruncatesLargeFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.txt")
	big := strings.Repeat("x", 1000)
	if err := os.WriteFile(f, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	mentions := Resolve(dir, []string{"big.txt"}, 100)
	if len(mentions) != 1 {
		t.Fatalf("len = %d", len(mentions))
	}
	if len(mentions[0].Content) <= 100 {
		t.Errorf("should be truncated: len=%d", len(mentions[0].Content))
	}
	if !strings.Contains(mentions[0].Content, "truncated") {
		t.Errorf("should mention truncation: %q", mentions[0].Content)
	}
}

func TestResolve_MissingFile(t *testing.T) {
	mentions := Resolve(t.TempDir(), []string{"nope.txt"}, 0)
	if len(mentions) != 1 {
		t.Fatalf("len = %d", len(mentions))
	}
	if !strings.Contains(mentions[0].Content, "error") {
		t.Errorf("should contain error: %q", mentions[0].Content)
	}
	if mentions[0].Tokens != 0 {
		t.Errorf("tokens should be 0 for error: %d", mentions[0].Tokens)
	}
}

func TestResolve_EmptyPaths(t *testing.T) {
	mentions := Resolve(t.TempDir(), nil, 0)
	if len(mentions) != 0 {
		t.Errorf("should be empty: %d", len(mentions))
	}
}

func TestFormatBlock_WithMentions(t *testing.T) {
	ments := []Mention{
		{Path: "a.go", Content: "package main", Tokens: 10},
		{Path: "b.go", Content: "func main(){}", Tokens: 20},
	}
	out := FormatBlock(ments, "do something")
	if !strings.Contains(out, "@a.go (10 tokens)") {
		t.Errorf("missing a.go header: %q", out)
	}
	if !strings.Contains(out, "package main") {
		t.Errorf("missing a.go content: %q", out)
	}
	if !strings.Contains(out, "@b.go (20 tokens)") {
		t.Errorf("missing b.go header: %q", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "do something") {
		t.Errorf("missing remaining text: %q", out)
	}
}

func TestFormatBlock_NoMentions(t *testing.T) {
	out := FormatBlock(nil, "plain text")
	if out != "plain text" {
		t.Errorf("should pass through: %q", out)
	}
}

func TestTotalTokens(t *testing.T) {
	ments := []Mention{
		{Tokens: 10},
		{Tokens: 20},
		{Tokens: 5},
	}
	if got := TotalTokens(ments); got != 35 {
		t.Errorf("TotalTokens = %d, want 35", got)
	}
}

func TestTotalTokens_Empty(t *testing.T) {
	if got := TotalTokens(nil); got != 0 {
		t.Errorf("TotalTokens = %d, want 0", got)
	}
}
