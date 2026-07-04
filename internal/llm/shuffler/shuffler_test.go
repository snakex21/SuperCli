package shuffler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	content := "# a comment\n" +
		"http://1.2.3.4:8080\n" +
		"\n" + // blank line skipped
		"5.6.7.8:3128\n" + // bare host:port normalized to http://
		"# another comment\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s := New()
	if err := s.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	got := s.List()
	want := []string{"http://1.2.3.4:8080", "http://5.6.7.8:3128"}
	if len(got) != len(want) {
		t.Fatalf("proxies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("proxy[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadFromFile_MissingFile(t *testing.T) {
	s := New()
	if err := s.LoadFromFile(filepath.Join(t.TempDir(), "nope.txt")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadFromFile_NoValidProxies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte("# only comments\n\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	s := New()
	if err := s.LoadFromFile(path); err == nil {
		t.Fatal("expected error when no valid proxies found")
	}
}
