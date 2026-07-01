package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsUnder(t *testing.T) {
	cases := []struct {
		parent string
		child  string
		want   bool
	}{
		{"/home/u", "/home/u", true},
		{"/home/u", "/home/u/file", true},
		{"/home/u", "/home/u/dir/file", true},
		{"/home/u", "/home/u2/file", false},
		{"/home/u", "/etc/passwd", false},
		{"/home/u", "/home", false},
		// Relative escaping
		{"/home/u", "/home/u/../etc", false},
	}
	for _, c := range cases {
		got := IsUnder(c.parent, c.child)
		if got != c.want {
			t.Errorf("IsUnder(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
		}
	}
}

func TestResolveSafe_EmptyHome(t *testing.T) {
	_, err := ResolveSafe("", "file")
	if err == nil {
		t.Error("expected error for empty home")
	}
}

func TestResolveSafe_Relative(t *testing.T) {
	home := t.TempDir()
	got, err := ResolveSafe(home, "foo/bar")
	if err != nil {
		t.Fatalf("ResolveSafe: %v", err)
	}
	want := filepath.Join(home, "foo", "bar")
	if got != want {
		t.Errorf("ResolveSafe = %q, want %q", got, want)
	}
}

func TestResolveSafe_EmptyRelReturnsHome(t *testing.T) {
	home := t.TempDir()
	got, err := ResolveSafe(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Errorf("empty rel = %q, want %q", got, home)
	}
}

func TestResolveSafe_DotEscape(t *testing.T) {
	home := t.TempDir()
	_, err := ResolveSafe(home, "../etc/passwd")
	if err != ErrEscape {
		t.Errorf("expected ErrEscape, got %v", err)
	}
}

func TestResolveSafe_AbsoluteOutside(t *testing.T) {
	home := t.TempDir()
	// Use a path that is absolute on the current OS.
	abs := "/etc/passwd"
	if filepath.Separator == '\\' {
		// On Windows, a leading '/' is NOT absolute
		// (filepath.IsAbs returns false). Use a
		// drive-letter path that lives outside the
		// temp dir.
		abs = `C:\Windows\System32\drivers\etc\hosts`
	}
	_, err := ResolveSafe(home, abs)
	if err != ErrEscape {
		t.Errorf("expected ErrEscape for absolute outside (%q), got %v", abs, err)
	}
}

func TestResolveSafe_SymlinkEscape(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("symlink test is Unix-only")
	}
	home := t.TempDir()
	outside := t.TempDir()
	// Create a symlink inside home pointing outside.
	link := filepath.Join(home, "evil")
	if err := symlinkLink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	_, err := ResolveSafe(home, "evil")
	if err != ErrEscape {
		t.Errorf("expected ErrEscape for symlink escape, got %v", err)
	}
}

func TestResolveSafe_NonExistentPath(t *testing.T) {
	home := t.TempDir()
	// Path that doesn't exist should still resolve
	// safely (it stays inside home).
	got, err := ResolveSafe(home, "a/b/c/d.txt")
	if err != nil {
		t.Fatalf("ResolveSafe: %v", err)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("expected path under %q, got %q", home, got)
	}
}

func TestResolveSafe_SensitiveRoot(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("Unix-only sensitive root test")
	}
	home := t.TempDir()
	// Even an absolute path to a sensitive root must
	// be refused, even if the home allows it.
	_, err := ResolveSafe(home, "/dev/null")
	if err != ErrDenied {
		t.Errorf("expected ErrDenied for /dev/null, got %v", err)
	}
	_, err = ResolveSafe(home, "/etc/hosts")
	if err != ErrDenied {
		t.Errorf("expected ErrDenied for /etc/hosts, got %v", err)
	}
	_, err = ResolveSafe(home, "/proc/cpuinfo")
	if err != ErrDenied {
		t.Errorf("expected ErrDenied for /proc/cpuinfo, got %v", err)
	}
}

func TestIsSensitive(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("Unix-only")
	}
	cases := []struct {
		path string
		want bool
	}{
		{"/dev", true},
		{"/dev/null", true},
		{"/etc/hosts", true},
		{"/etc", true},
		{"/proc/cpuinfo", true},
		{"/sys/kernel", true},
		{"/var/log", true},
		{"/home/user", false},
		{"/tmp", false},
		{"/", true}, // root is sensitive
	}
	for _, c := range cases {
		if got := isSensitive(c.path); got != c.want {
			t.Errorf("isSensitive(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestResolveSafe_UnsandboxedAllowsEscape(t *testing.T) {
	home := t.TempDir()
	prev := Unsandboxed
	Unsandboxed = true
	defer func() { Unsandboxed = prev }()

	// Absolute path outside home should succeed when unsandboxed.
	got, err := ResolveSafe(home, "/tmp")
	if err != nil {
		t.Fatalf("unsandboxed ResolveSafe outside home: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty path")
	}
}

func TestResolveSafe_UnsandboxedStillBlocksSensitive(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("Unix-only")
	}
	home := t.TempDir()
	prev := Unsandboxed
	Unsandboxed = true
	defer func() { Unsandboxed = prev }()

	_, err := ResolveSafe(home, "/etc/hosts")
	if err != ErrDenied {
		t.Errorf("unsandboxed should still block sensitive paths, got %v", err)
	}
}
