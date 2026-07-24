package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeFile(t *testing.T) {
	cases := []struct {
		scope    string
		wantTail string
		wantErr  bool
	}{
		{"general", "general.md", false},
		{"project:abc12345", "project-abc12345.md", false},
		{"scratch:2026-06-06", "scratch-2026-06-06.md", false},
		{"pattern:auth-1", filepath.Join("patterns", "auth-1.md"), false},
		{"unknown-scope", "scope-unknown-scope.md", false},
		{"project:", "", true},
		{"scratch:", "", true},
		{"pattern:", "", true},
	}
	for _, c := range cases {
		got, gotScope, err := ScopeFile("/root", c.scope)
		if c.wantErr {
			if err == nil {
				t.Errorf("scope=%q expected error, got %q", c.scope, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("scope=%q: %v", c.scope, err)
			continue
		}
		if gotScope != c.scope {
			t.Errorf("scope=%q: returned scope = %q", c.scope, gotScope)
		}
		// Match by last path segment(s) so the test is
		// platform-agnostic.
		if !sameTail(got, c.wantTail) {
			t.Errorf("scope=%q: got %q, want tail %q", c.scope, got, c.wantTail)
		}
	}
}

func TestScopeFile_RejectsEmpty(t *testing.T) {
	if _, _, err := ScopeFile("/root", ""); err == nil {
		t.Fatal("expected error for empty scope")
	}
	if _, _, err := ScopeFile("", "general"); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestProjectHash_Deterministic(t *testing.T) {
	a := ProjectHash("C:/foo/bar")
	b := ProjectHash("C:/foo/bar")
	c := ProjectHash("C:/baz")
	if a != b {
		t.Errorf("hash not stable: %s vs %s", a, b)
	}
	if a == c {
		t.Errorf("hash collision: %s", a)
	}
	if len(a) != 8 {
		t.Errorf("hash length = %d, want 8", len(a))
	}
}

func TestEntry_Validate(t *testing.T) {
	cases := []struct {
		name   string
		entry  Entry
		wantOk bool
	}{
		{"ok", Entry{ID: "1", Scope: "general", Content: "x", Source: SourceUser}, true},
		{"empty_id", Entry{Scope: "general", Content: "x"}, false},
		{"empty_content", Entry{ID: "1", Scope: "general"}, false},
		{"empty_scope", Entry{ID: "1", Content: "x"}, false},
		{"empty_source_ok", Entry{ID: "1", Scope: "general", Content: "x"}, true},
		{"bad_source", Entry{ID: "1", Scope: "general", Content: "x", Source: "banana"}, false},
	}
	for _, c := range cases {
		err := c.entry.Validate()
		if c.wantOk && err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
		if !c.wantOk && err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestEntry_TagsRoundtrip(t *testing.T) {
	e := Entry{Tags: []string{"a", "b", "c"}}
	csv := e.TagsCSV()
	back := EntriesFromCSV(csv)
	if len(back) != 3 || back[0] != "a" || back[2] != "c" {
		t.Errorf("roundtrip: %v", back)
	}
}

func TestEntriesFromCSV_Empty(t *testing.T) {
	if got := EntriesFromCSV(""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := EntriesFromCSV(",,,"); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestSanitise(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc-123", "abc-123"},
		{"with spaces", "with_spaces"},
		{"path/traversal/../", "path_traversal_.._"},
		{"a/b", "a_b"},
	}
	for _, c := range cases {
		if got := sanitise(c.in); got != c.want {
			t.Errorf("sanitise(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// sameTail returns true if path ends with suffix when both
// separators are normalised to '/'.
func sameTail(path, suffix string) bool {
	a := strings.ReplaceAll(path, "\\", "/")
	b := strings.ReplaceAll(suffix, "\\", "/")
	return strings.HasSuffix(a, b)
}
