package tui

import (
	"strings"
	"testing"
)

func TestStatusBar_Render_AllSections(t *testing.T) {
	p := DefaultPalette()
	sb := StatusBar{
		Model:     "gpt-4o",
		Credits:   "1.2k/10k (12%)",
		Goal:      "3/5 tasks",
		DraftMode: "on",
		Session:   "abc123",
		Width:     120,
	}
	rendered := sb.Render(p)
	if !strings.Contains(rendered, "gpt-4o") {
		t.Fatalf("missing model: %q", rendered)
	}
	if !strings.Contains(rendered, "1.2k/10k (12%)") {
		t.Fatalf("missing credits: %q", rendered)
	}
	if !strings.Contains(rendered, "3/5 tasks") {
		t.Fatalf("missing goal: %q", rendered)
	}
	if !strings.Contains(rendered, "on") {
		t.Fatalf("missing draft: %q", rendered)
	}
	if !strings.Contains(rendered, "abc123") {
		t.Fatalf("missing session: %q", rendered)
	}
}

func TestStatusBar_Render_SkipsEmpty(t *testing.T) {
	p := DefaultPalette()
	sb := StatusBar{
		Model:   "gpt-4o",
		Credits: "50%",
		Width:   120,
	}
	rendered := sb.Render(p)
	if strings.Contains(rendered, "goal") {
		t.Fatalf("should not contain goal section: %q", rendered)
	}
	if strings.Contains(rendered, "draft") {
		t.Fatalf("should not contain draft section: %q", rendered)
	}
}

func TestStatusBar_Render_Separators(t *testing.T) {
	p := DefaultPalette()
	sb := StatusBar{
		Model:   "m",
		Credits: "c",
		Width:   120,
	}
	rendered := sb.Render(p)
	if !strings.Contains(rendered, "│") {
		t.Fatalf("missing pipe separator: %q", rendered)
	}
}

func TestStatusBar_Render_TruncatesWidth(t *testing.T) {
	p := DefaultPalette()
	sb := StatusBar{
		Model:   strings.Repeat("x", 50),
		Credits: strings.Repeat("y", 50),
		Width:   20,
	}
	rendered := sb.Render(p)
	runes := []rune(rendered)
	if len(runes) > 20 {
		t.Fatalf("rendered %d runes, max 20: %q", len(runes), rendered)
	}
	if runes[len(runes)-1] != '…' {
		t.Fatalf("truncated line should end with …: %q", rendered)
	}
}

func TestFormatCredits(t *testing.T) {
	tests := []struct {
		used, total int
		want        string
	}{
		{0, 10000, "0/10.0k (0%)"},
		{1200, 10000, "1.2k/10.0k (12%)"},
		{10000, 10000, "10.0k/10.0k (100%)"},
		{500, 1000, "500/1.0k (50%)"},
	}
	for _, tt := range tests {
		got := FormatCredits(tt.used, tt.total)
		if got != tt.want {
			t.Errorf("FormatCredits(%d, %d) = %q, want %q", tt.used, tt.total, got, tt.want)
		}
	}
}

func TestFormatCredits_ZeroTotal(t *testing.T) {
	if got := FormatCredits(0, 0); got != "" {
		t.Errorf("zero total should return empty, got %q", got)
	}
}

func TestCompactNum(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{1200, "1.2k"},
		{999999, "1000.0k"},
		{1000000, "1.0m"},
		{2500000, "2.5m"},
	}
	for _, tt := range tests {
		got := compactNum(tt.n)
		if got != tt.want {
			t.Errorf("compactNum(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestStatusBar_Render_TokensAndCost(t *testing.T) {
	p := DefaultPalette()
	sb := StatusBar{
		Model:  "gpt-4o",
		Tokens: "1.2k",
		Cost:   "$0.003",
		Width:  120,
	}
	rendered := sb.Render(p)
	if !strings.Contains(rendered, "1.2k") {
		t.Fatalf("missing tokens: %q", rendered)
	}
	if !strings.Contains(rendered, "$0.003") {
		t.Fatalf("missing cost: %q", rendered)
	}
	// Should show "tok:" label.
	if !strings.Contains(rendered, "tok") {
		t.Fatalf("missing tok label: %q", rendered)
	}
}

func TestStatusBar_Render_TokensOnly(t *testing.T) {
	p := DefaultPalette()
	sb := StatusBar{
		Tokens: "500",
		Width:  120,
	}
	rendered := sb.Render(p)
	if !strings.Contains(rendered, "500") {
		t.Fatalf("missing tokens: %q", rendered)
	}
}
