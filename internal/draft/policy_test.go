package draft

import (
	"testing"
)

func TestParseMode_RoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
		err  bool
	}{
		{"", ModeOff, false},
		{"off", ModeOff, false},
		{"OFF", ModeOff, false},
		{"always", ModeAlways, false},
		{"balanced", ModeBalanced, false},
		{"critical", ModeCriticalOnly, false},
		{"critical_only", ModeCriticalOnly, false},
		{"critical-only", ModeCriticalOnly, false},
		{"nonsense", ModeOff, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseMode(c.in)
			if (err != nil) != c.err {
				t.Fatalf("err = %v, want err=%v", err, c.err)
			}
			if !c.err && got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestModeString(t *testing.T) {
	cases := []struct {
		m    Mode
		want string
	}{
		{ModeOff, "off"},
		{ModeAlways, "always"},
		{ModeBalanced, "balanced"},
		{ModeCriticalOnly, "critical_only"},
		{Mode(99), "unknown(99)"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := c.m.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNewPolicy_RejectsEqualModels(t *testing.T) {
	_, err := NewPolicy(ModeAlways, "gpt-4o", "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error when draft == main")
	}
}

func TestNewPolicy_EmptyMainIsError(t *testing.T) {
	_, err := NewPolicy(ModeAlways, "x", "", nil)
	if err == nil {
		t.Fatal("expected error for empty mainModel")
	}
}

func TestNewPolicy_OffIsValid(t *testing.T) {
	p, err := NewPolicy(ModeOff, "", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ModeOff {
		t.Errorf("got %v, want Off", p.Mode)
	}
	if p.Drafted == nil {
		t.Error("Drafted map should be initialized even for Off policy")
	}
}

func TestNewPolicy_DefaultCriticalTools(t *testing.T) {
	p, err := NewPolicy(ModeCriticalOnly, "draft", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.CriticalTools) == 0 {
		t.Fatal("default CriticalTools should be populated")
	}
	if !p.IsCritical("write_file") {
		t.Error("write_file should be critical by default")
	}
	if !p.IsCritical("bash") {
		t.Error("bash should be critical by default")
	}
	if p.IsCritical("read_file") {
		t.Error("read_file should NOT be critical by default")
	}
}

func TestNewPolicy_CustomCriticalTools(t *testing.T) {
	p, err := NewPolicy(ModeCriticalOnly, "draft", "main", []string{"only-this"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsCritical("only-this") {
		t.Error("only-this should be critical")
	}
	if p.IsCritical("write_file") {
		t.Error("write_file should NOT be critical with custom list")
	}
}

func TestPolicy_ShouldDraft_ModeOff(t *testing.T) {
	p, _ := NewPolicy(ModeOff, "", "main", nil)
	ok, reason := p.ShouldDraft(0, nil)
	if ok {
		t.Error("ModeOff should never draft")
	}
	if reason != "off" {
		t.Errorf("reason = %q, want off", reason)
	}
}

func TestPolicy_ShouldDraft_Always(t *testing.T) {
	p, _ := NewPolicy(ModeAlways, "draft", "main", nil)
	ok, reason := p.ShouldDraft(0, nil)
	if !ok {
		t.Error("ModeAlways should always draft")
	}
	if reason != "always" {
		t.Errorf("reason = %q, want always", reason)
	}
}

func TestPolicy_ShouldDraft_BalancedFirstStep(t *testing.T) {
	p, _ := NewPolicy(ModeBalanced, "draft", "main", nil)
	ok, _ := p.ShouldDraft(0, nil)
	if !ok {
		t.Error("ModeBalanced step 0 should draft")
	}
}

func TestPolicy_ShouldDraft_BalancedSkipAfterMarked(t *testing.T) {
	p, _ := NewPolicy(ModeBalanced, "draft", "main", nil)
	p.MarkDrafted(0)
	ok, reason := p.ShouldDraft(1, nil)
	if ok {
		t.Error("ModeBalanced should skip after step marked")
	}
	if reason != "already-drafted" {
		t.Errorf("reason = %q, want already-drafted", reason)
	}
}

func TestPolicy_ShouldDraft_CriticalOnlyNoToolCall(t *testing.T) {
	p, _ := NewPolicy(ModeCriticalOnly, "draft", "main", []string{"write_file"})
	ok, reason := p.ShouldDraft(0, nil)
	if ok {
		t.Error("ModeCriticalOnly with no tool call should not draft")
	}
	if reason != "no-tool-call" {
		t.Errorf("reason = %q, want no-tool-call", reason)
	}
}

func TestPolicy_ShouldDraft_CriticalOnlyMatch(t *testing.T) {
	p, _ := NewPolicy(ModeCriticalOnly, "draft", "main", []string{"write_file"})
	ok, reason := p.ShouldDraft(0, []string{"read_file", "write_file"})
	if !ok {
		t.Error("should draft on write_file")
	}
	if reason != "critical-tool" {
		t.Errorf("reason = %q, want critical-tool", reason)
	}
}

func TestPolicy_ShouldDraft_CriticalOnlyMCPMatch(t *testing.T) {
	p, _ := NewPolicy(ModeCriticalOnly, "draft", "main", []string{"write_file"})
	ok, _ := p.ShouldDraft(0, []string{"mcp__github_create_pr"})
	if !ok {
		t.Error("mcp__* tools should always be critical")
	}
}

func TestPolicy_ShouldDraft_CriticalOnlyNoMatch(t *testing.T) {
	p, _ := NewPolicy(ModeCriticalOnly, "draft", "main", []string{"write_file"})
	ok, reason := p.ShouldDraft(0, []string{"read_file", "list_dir"})
	if ok {
		t.Error("read-only tools should not trigger draft")
	}
	if reason != "not-critical" {
		t.Errorf("reason = %q, want not-critical", reason)
	}
}

func TestPolicy_NilSafe(t *testing.T) {
	var p *Policy
	ok, _ := p.ShouldDraft(0, nil)
	if ok {
		t.Error("nil policy should never draft")
	}
	if p.IsCritical("write_file") {
		t.Error("nil policy IsCritical should be false")
	}
	// MarkDrafted on nil is a no-op (no panic).
	p.MarkDrafted(0)
}

func TestPolicy_MarkDrafted_Idempotent(t *testing.T) {
	p, _ := NewPolicy(ModeBalanced, "draft", "main", nil)
	p.MarkDrafted(5)
	p.MarkDrafted(5)
	if len(*p.Drafted) != 1 {
		t.Errorf("MarkDrafted should be idempotent; got %d entries", len(*p.Drafted))
	}
}
