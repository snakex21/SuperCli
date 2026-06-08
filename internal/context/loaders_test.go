package context

import (
	"os"
	"strings"
	"testing"
)

func TestFileLoader_LoadsAllFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/CLAUDE.md", "be terse")
	mustWrite(t, dir+"/AGENTS.md", "use TDD")
	mustWrite(t, dir+"/README.md", "SuperCli")
	mustWrite(t, dir+"/.gitignore", "*.exe")
	l := NewFileLoader(dir)
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "project_notes" {
		t.Errorf("Name = %q", s.Name)
	}
	for _, want := range []string{"CLAUDE.md", "AGENTS.md", "README.md", ".gitignore"} {
		if !strings.Contains(s.Body, want) {
			t.Errorf("Body missing %s: %q", want, s.Body)
		}
	}
}

func TestFileLoader_NoFilesReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	l := NewFileLoader(dir)
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Body != "" {
		t.Errorf("expected empty body, got %q", s.Body)
	}
}

func TestFileLoader_TruncatesOversizeFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("a", 20*1024) // 20 KiB
	mustWrite(t, dir+"/CLAUDE.md", big)
	l := NewFileLoader(dir)
	l.MaxFileBytes = 1024
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(s.Body, "truncated") {
		t.Errorf("expected truncation marker in body")
	}
}

func TestFileLoader_EmptyHome(t *testing.T) {
	l := NewFileLoader("")
	_, err := l.Load()
	if err == nil {
		t.Fatal("expected error on empty home")
	}
}

func TestCwdTree_IncludesTopLevel(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir+"/main.go", "package main")
	mustMkdir(t, dir+"/src")
	mustWrite(t, dir+"/src/foo.go", "package x")
	l := NewCwdTreeLoader(dir)
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(s.Body, "src/") {
		t.Errorf("Body missing src/: %q", s.Body)
	}
}

func TestCwdTree_SkipsIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, dir+"/node_modules")
	mustWrite(t, dir+"/node_modules/foo.js", "x")
	mustWrite(t, dir+"/main.go", "x")
	l := NewCwdTreeLoader(dir)
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(s.Body, "node_modules") {
		t.Errorf("Body should not contain node_modules: %q", s.Body)
	}
}

func TestCwdTree_EmptyHome(t *testing.T) {
	l := NewCwdTreeLoader("")
	_, err := l.Load()
	if err == nil {
		t.Fatal("expected error on empty home")
	}
}

func TestCwdTree_TruncatesBeyondMaxEntries(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		mustMkdir(t, dir+"/d"+itoa(i))
	}
	l := NewCwdTreeLoader(dir)
	l.MaxEntries = 5
	s, _ := l.Load()
	if !strings.Contains(s.Body, "truncated") {
		t.Errorf("expected truncation marker: %q", s.Body)
	}
}

func TestGitStatus_NotARepoReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	l := NewGitStatusLoader(dir)
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Body != "" {
		t.Errorf("expected empty body for non-repo, got %q", s.Body)
	}
}

func TestEnvLoader_FormatsKeys(t *testing.T) {
	t.Setenv("SUPERCLI_TEST_VAR", "value123")
	l := NewEnvLoader()
	l.Keys = []string{"SUPERCLI_TEST_VAR"}
	s, err := l.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(s.Body, "SUPERCLI_TEST_VAR=value123") {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestEnvLoader_RedactsSecretKeys(t *testing.T) {
	t.Setenv("SUPERCLI_TEST_API_KEY", "sk-secret")
	l := NewEnvLoader()
	l.Keys = []string{"SUPERCLI_TEST_API_KEY"}
	s, _ := l.Load()
	if strings.Contains(s.Body, "sk-secret") {
		t.Errorf("Body leaked secret: %q", s.Body)
	}
	if !strings.Contains(s.Body, "redacted") {
		t.Errorf("Body missing redaction: %q", s.Body)
	}
}

func TestEnvLoader_EmptyWhenNoKeys(t *testing.T) {
	l := NewEnvLoader()
	l.Keys = []string{"UNLIKELY_TO_EXIST_X9Y8Z7"}
	s, _ := l.Load()
	if s.Body != "" {
		t.Errorf("expected empty body, got %q", s.Body)
	}
}

func TestEnvLoader_TruncatesPath(t *testing.T) {
	t.Setenv("PATH", "/a:/b:/c:/d:/e:/f:/g")
	l := NewEnvLoader()
	s, _ := l.Load()
	if strings.Count(s.Body, "/") > 8 {
		t.Errorf("PATH not truncated: %q", s.Body)
	}
}

func TestIsSecretKey(t *testing.T) {
	cases := []struct {
		k    string
		want bool
	}{
		{"FOO", false},
		{"API_KEY", true},
		{"my_secret", true},
		{"TOKEN", true},
		{"password", true},
		{"GIT_TOKEN", true},
		{"PATH", false},
	}
	for _, c := range cases {
		if got := isSecretKey(c.k); got != c.want {
			t.Errorf("isSecretKey(%q) = %v, want %v", c.k, got, c.want)
		}
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var s []byte
	for n > 0 {
		s = append([]byte{byte('0' + n%10)}, s...)
		n /= 10
	}
	return string(s)
}
