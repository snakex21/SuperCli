package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkSkillDir(t *testing.T, base, name, content string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestDiscoverer_FindsSkillsInProjectDir(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "alpha", "---\ndescription: alpha skill\n---\n# Alpha\ndo alpha things\n")
	mkSkillDir(t, filepath.Join(project, "skills"), "beta", "---\ndescription: beta skill\n---\n# Beta\ndo beta things\n")
	d := NewDiscoverer(project, user)
	skills, err := d.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(skills))
	}
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
		if s.Description == "" {
			t.Errorf("skill %q missing description", s.Name)
		}
		if s.Content == "" {
			t.Errorf("skill %q missing content", s.Name)
		}
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("missing alpha/beta: %v", names)
	}
}

func TestDiscoverer_PriorityOrder(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	// Same name in both project and user — project wins.
	mkSkillDir(t, filepath.Join(project, "skills"), "shared", "---\ndescription: from project\n---\nproject content")
	mkSkillDir(t, filepath.Join(user, "skills"), "shared", "---\ndescription: from user\n---\nuser content")
	d := NewDiscoverer(project, user)
	skills, err := d.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1 (deduped)", len(skills))
	}
	if !strings.Contains(skills[0].Content, "project content") {
		t.Errorf("project content not winning: %q", skills[0].Content)
	}
}

func TestDiscoverer_SkipsDirsWithoutSKILLmd(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "alpha", "alpha content")
	// Create a dir with no SKILL.md — should be silently skipped.
	noSkill := filepath.Join(project, "skills", "no-skill")
	if err := os.MkdirAll(noSkill, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	d := NewDiscoverer(project, user)
	skills, _ := d.Discover()
	if len(skills) != 1 {
		t.Errorf("got %d, want 1 (no-skill dir should be skipped)", len(skills))
	}
}

func TestDiscoverer_ParsesFrontmatter(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	content := "---\nname: code-review\ndescription: Run a code review\ntags: [review, go, quality]\n---\n# Code Review\n\nFollow these steps...\n"
	mkSkillDir(t, filepath.Join(project, "skills"), "code-review", content)
	d := NewDiscoverer(project, user)
	skills, _ := d.Discover()
	if len(skills) != 1 {
		t.Fatalf("got %d, want 1", len(skills))
	}
	s := skills[0]
	if s.Name != "code-review" {
		t.Errorf("Name = %q, want code-review", s.Name)
	}
	if s.Description != "Run a code review" {
		t.Errorf("Description = %q", s.Description)
	}
	if len(s.Tags) != 3 || s.Tags[0] != "review" {
		t.Errorf("Tags = %v, want [review, go, quality]", s.Tags)
	}
	if !strings.Contains(s.Content, "Follow these steps") {
		t.Errorf("Content = %q", s.Content)
	}
	if !strings.HasPrefix(strings.TrimSpace(s.Content), "#") {
		t.Errorf("frontmatter should be stripped, got: %q", s.Content)
	}
}

func TestDiscoverer_NoFrontmatter_FallsBackToFirstLine(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "plain", "This is a one-line description.\n\n# Body\n...")
	d := NewDiscoverer(project, user)
	skills, _ := d.Discover()
	if skills[0].Description != "This is a one-line description." {
		t.Errorf("Description = %q, want fallback", skills[0].Description)
	}
}

func TestSkillApplier_ApplyOnceAppends(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "alpha", "# Alpha\nalpha content here")
	d := NewDiscoverer(project, user)
	applier := NewSkillApplier(d)
	res, err := applier.execute(context.Background(), json.RawMessage(`{"name":"alpha"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err: %v", res.Err)
	}
	combined := applier.AppendSkills()
	if !strings.Contains(combined, "## Skill: alpha") {
		t.Errorf("AppendSkills missing header: %q", combined)
	}
	if !strings.Contains(combined, "alpha content here") {
		t.Errorf("AppendSkills missing content: %q", combined)
	}
}

func TestSkillApplier_DuplicateIsNoop(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "alpha", "alpha")
	d := NewDiscoverer(project, user)
	applier := NewSkillApplier(d)
	_, _ = applier.execute(context.Background(), json.RawMessage(`{"name":"alpha"}`))
	res, _ := applier.execute(context.Background(), json.RawMessage(`{"name":"alpha"}`))
	if !strings.Contains(res.Text, "already applied") {
		t.Errorf("expected already-applied message, got %q", res.Text)
	}
	if len(applier.Applied()) != 1 {
		t.Errorf("Applied = %v, want 1 entry", applier.Applied())
	}
}

func TestSkillApplier_UnknownSkill(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	d := NewDiscoverer(project, user)
	applier := NewSkillApplier(d)
	res, _ := applier.execute(context.Background(), json.RawMessage(`{"name":"nope"}`))
	if res.Err == nil {
		t.Error("expected error for unknown skill")
	}
}

func TestSkillApplier_EmptyName(t *testing.T) {
	d := NewDiscoverer(t.TempDir(), t.TempDir())
	applier := NewSkillApplier(d)
	res, _ := applier.execute(context.Background(), json.RawMessage(`{"name":""}`))
	if res.Err == nil {
		t.Error("expected error for empty name")
	}
}

func TestSkillApplier_MultipleSkillsPreserveOrder(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "a", "A")
	mkSkillDir(t, filepath.Join(project, "skills"), "b", "B")
	mkSkillDir(t, filepath.Join(project, "skills"), "c", "C")
	d := NewDiscoverer(project, user)
	applier := NewSkillApplier(d)
	_, _ = applier.execute(context.Background(), json.RawMessage(`{"name":"b"}`))
	_, _ = applier.execute(context.Background(), json.RawMessage(`{"name":"a"}`))
	_, _ = applier.execute(context.Background(), json.RawMessage(`{"name":"c"}`))
	got := applier.Applied()
	want := []string{"b", "a", "c"}
	if len(got) != 3 {
		t.Fatalf("Applied = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Applied[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSkillApplier_Reset(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "x", "X")
	d := NewDiscoverer(project, user)
	applier := NewSkillApplier(d)
	_, _ = applier.execute(context.Background(), json.RawMessage(`{"name":"x"}`))
	applier.Reset()
	if len(applier.Applied()) != 0 {
		t.Errorf("Applied = %v after Reset, want empty", applier.Applied())
	}
	if applier.AppendSkills() != "" {
		t.Error("AppendSkills non-empty after Reset")
	}
}
