package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectKey_StableAndCollisionFree(t *testing.T) {
	a := filepath.FromSlash("/home/u/work/api")
	if ProjectKey(a) != ProjectKey(a) {
		t.Fatal("ProjectKey is not stable for the same path")
	}
	b := filepath.FromSlash("/home/u/other/api")
	if ProjectKey(a) == ProjectKey(b) {
		t.Fatalf("same basename, different paths must not collide: %s", ProjectKey(a))
	}
	if !strings.HasPrefix(ProjectKey(a), "api-") {
		t.Fatalf("key should start with basename: %s", ProjectKey(a))
	}
	parts := strings.Split(ProjectKey(a), "-")
	if h := parts[len(parts)-1]; len(h) != 8 {
		t.Fatalf("hash suffix should be 8 chars, got %q", h)
	}
}

func TestProjectKey_SanitisesWeirdNames(t *testing.T) {
	k := ProjectKey(filepath.FromSlash("/tmp/my project (v2)!"))
	if strings.ContainsAny(k, " ()!") {
		t.Fatalf("key not sanitised: %q", k)
	}
}

func TestProjectsMap_RoundTripAndRepair(t *testing.T) {
	home := t.TempDir()
	m := map[string]string{"C:/x": "x-12345678"}
	if err := SaveProjectsMap(home, m); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := LoadProjectsMap(home)
	if got["C:/x"] != "x-12345678" {
		t.Fatalf("round trip lost data: %v", got)
	}
}

func TestOpenProjectStore_CreatesDBAndMapping(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "someproj")
	s, err := OpenProjectStore(home, proj)
	if err != nil {
		t.Fatalf("OpenProjectStore: %v", err)
	}
	defer s.Close()
	m := LoadProjectsMap(home)
	if m[proj] != ProjectKey(proj) {
		t.Fatalf("projects.json not updated: %v", m)
	}
	if !strings.Contains(filepath.ToSlash(s.Root()), "/projects/") {
		t.Fatalf("store root should live under projects/: %s", s.Root())
	}
}
