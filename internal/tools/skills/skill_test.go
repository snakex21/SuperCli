package skills

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

func writeTestBuiltinPack(t *testing.T, dataDir string) {
	t.Helper()
	dir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "builtin-skills.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("skills/007/SKILL.md")
	if err == nil {
		_, err = w.Write([]byte("# 007\nRun a careful security audit."))
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("write test builtin pack: %v", err)
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
	if !strings.Contains(res.Text, "<skill-guidance>") || !strings.Contains(res.Text, "alpha content here") {
		t.Fatalf("applied guidance did not reach the tool result: %q", res.Text)
	}
	combined := applier.AppendSkills()
	if !strings.Contains(combined, "## Skill: alpha") {
		t.Errorf("AppendSkills missing header: %q", combined)
	}
	if !strings.Contains(combined, "alpha content here") {
		t.Errorf("AppendSkills missing content: %q", combined)
	}
}

func TestSkillApplier_SearchDoesNotApply(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	mkSkillDir(t, filepath.Join(project, "skills"), "code-review", "---\ndescription: Review Go code for correctness\n---\nreview")
	mkSkillDir(t, filepath.Join(project, "skills"), "image-work", "---\ndescription: Create raster images\n---\nimage")
	applier := NewSkillApplier(NewDiscoverer(project, user))
	res, err := applier.execute(context.Background(), json.RawMessage(`{"query":"review code","limit":2}`))
	if err != nil || res.Err != nil {
		t.Fatalf("search: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, `"name":"code-review"`) {
		t.Fatalf("search result = %s", res.Text)
	}
	if len(applier.Applied()) != 0 {
		t.Fatalf("search activated skills: %v", applier.Applied())
	}
}

func TestSkillApplier_CapsLargeGuidanceUTF8Safely(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	content := strings.Repeat("ważna instrukcja\n", 5000)
	mkSkillDir(t, filepath.Join(project, "skills"), "large", content)
	applier := NewSkillApplier(NewDiscoverer(project, user))
	res, _ := applier.execute(context.Background(), json.RawMessage(`{"name":"large"}`))
	if res.Err != nil {
		t.Fatalf("apply large: %v", res.Err)
	}
	if !strings.Contains(res.Text, "omitted_bytes=") {
		t.Fatalf("large guidance was not capped")
	}
	if !strings.Contains(res.Text, "ważna instrukcja") || !utf8.ValidString(res.Text) {
		t.Fatalf("large guidance is not valid UTF-8")
	}
}

func TestBuiltinPack_IsLazyAndMaterializesSelectedSkill(t *testing.T) {
	dataDir := t.TempDir()
	writeTestBuiltinPack(t, dataDir)
	d := NewDiscovererWithBuiltins(t.TempDir(), dataDir)
	catalog, err := d.Discover()
	if err != nil {
		t.Fatalf("Discover builtins: %v", err)
	}
	if len(catalog) < 1400 {
		t.Fatalf("builtin catalog has %d skills, want at least 1400", len(catalog))
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cache", "builtin-skills")); !os.IsNotExist(err) {
		t.Fatalf("Discover eagerly extracted builtin files: %v", err)
	}
	hits, err := d.Search("code review", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("Search builtins: hits=%v err=%v", hits, err)
	}
	for _, hit := range hits {
		if hit.Content != "" {
			t.Fatalf("search loaded body for %q", hit.Name)
		}
	}
	skill, err := d.Get("007")
	if err != nil {
		t.Fatalf("Get builtin 007: %v", err)
	}
	if skill.Content == "" || !strings.Contains(skill.Path, filepath.Join("cache", "builtin-skills")) {
		t.Fatalf("materialized skill = %+v", skill)
	}
	if _, err := os.Stat(skill.Path); err != nil {
		t.Fatalf("materialized SKILL.md: %v", err)
	}
}

func TestBuiltinPack_MissingArchiveDoesNotBreakCatalog(t *testing.T) {
	dataDir := t.TempDir()
	d := NewDiscovererWithBuiltins(t.TempDir(), dataDir)
	catalog, err := d.Discover()
	if err != nil || len(catalog) < 1400 {
		t.Fatalf("metadata catalog unavailable without archive: len=%d err=%v", len(catalog), err)
	}
	_, err = d.Get("007")
	if err == nil || !strings.Contains(err.Error(), "builtin skill pack is missing") {
		t.Fatalf("missing archive error = %v", err)
	}
}

func BenchmarkBuiltinSkillSearch(b *testing.B) {
	d := NewDiscovererWithBuiltins(b.TempDir(), b.TempDir())
	if _, err := d.Search("security audit", 5); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Search("security audit", 5); err != nil {
			b.Fatal(err)
		}
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
	if !strings.Contains(res.Text, "<skill-guidance>") || !strings.Contains(res.Text, "alpha") {
		t.Errorf("re-apply must return cached guidance for another worker, got %q", res.Text)
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
