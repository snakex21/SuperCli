package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func verifyCanonicalHome(t *testing.T, home string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "child.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, resolve := range map[string]func(string, string) (string, error){"safe": ResolveSafe, "within": ResolveWithin} {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"", "child.txt", filepath.Join(home, "child.txt"), filepath.Join(canonical, "child.txt"), filepath.Join("missing", "child.txt")} {
				got, err := resolve(home, path)
				want := canonical
				if path != "" {
					if filepath.IsAbs(path) {
						want = filepath.Join(canonical, "child.txt")
					} else {
						want = filepath.Join(canonical, path)
					}
				}
				if err != nil || got != want {
					t.Errorf("resolve(%q, %q) = %q, %v; want %q", home, path, got, err, want)
				}
			}
			for _, outside := range []string{canonical + "-sibling", filepath.Join("..", "outside.txt")} {
				if got, err := resolve(home, outside); err != ErrEscape || got != "" {
					t.Errorf("outside %q = %q, %v; want ErrEscape", outside, got, err)
				}
			}
		})
	}
}

func TestResolveCanonicalSymlinkHome(t *testing.T) {
	root := t.TempDir()
	realHome, alias := filepath.Join(root, "real"), filepath.Join(root, "alias")
	if err := os.Mkdir(realHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, alias); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	verifyCanonicalHome(t, alias)
}

func TestResolveInvalidHomeFailsClosed(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{file, filepath.Join(file, "missing")} {
		for _, resolve := range []func(string, string) (string, error){ResolveSafe, ResolveWithin} {
			if got, err := resolve(home, "child.txt"); err == nil || got != "" {
				t.Errorf("invalid home %q = %q, %v; want closed failure", home, got, err)
			}
		}
	}
}

func TestResolveMissingHomeKeepsBoundary(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "new", "workspace")
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, resolve := range []func(string, string) (string, error){ResolveSafe, ResolveWithin} {
		want := filepath.Join(canonicalRoot, "new", "workspace", "child.txt")
		if got, err := resolve(home, "child.txt"); err != nil || got != want {
			t.Errorf("new workspace child = %q, %v; want %q", got, err, want)
		}
		if got, err := resolve(home, "../outside.txt"); err != ErrEscape || got != "" {
			t.Errorf("new workspace escape = %q, %v; want ErrEscape", got, err)
		}
	}
	prev := IsUnsandboxed()
	SetUnsandboxed(true)
	t.Cleanup(func() { SetUnsandboxed(prev) })
	if got, err := ResolveSafe(home, root); err != nil || got != canonicalRoot {
		t.Errorf("unsandboxed new workspace = %q, %v; want %q", got, err, canonicalRoot)
	}
}

func TestResolveDanglingSymlinkHomeFailsClosed(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), home); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	for _, candidate := range []string{home, filepath.Join(home, "child")} {
		if got, err := ResolveSafe(candidate, "file.txt"); err == nil || got != "" {
			t.Errorf("dangling home = %q, %v; want closed failure", got, err)
		}
	}
}

func TestResolveSymlinkLoopHomeFailsClosed(t *testing.T) {
	home := filepath.Join(t.TempDir(), "loop")
	if err := os.Symlink(home, home); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	for _, resolve := range []func(string, string) (string, error){ResolveSafe, ResolveWithin} {
		if got, err := resolve(home, "child.txt"); err == nil || got != "" {
			t.Errorf("symlink-loop home = %q, %v; want closed failure", got, err)
		}
	}
}
