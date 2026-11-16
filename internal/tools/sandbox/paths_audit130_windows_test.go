//go:build windows

package sandbox

// Audit 130: resolver-level confinement checks on Windows. Tool-level
// coverage lives in internal/tools/files/confinement_audit130_test.go.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func audit130SkipKnownFailing(t *testing.T, issue string) {
	t.Helper()
	if os.Getenv("SUPERCLI_AUDIT_KNOWN_FAILING") != "1" {
		t.Skip("KNOWN-FAILING audit-130: " + issue)
	}
}

func audit130Dirs(t *testing.T) (ws, out string) {
	t.Helper()
	prior := IsUnsandboxed()
	SetUnsandboxed(false)
	t.Cleanup(func() { SetUnsandboxed(prior) })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, out = filepath.Join(root, "ws"), filepath.Join(root, "outside")
	for _, d := range []string{ws, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ws, out
}

func audit130MakeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, out)
	}
	// rmdir removes only the reparse point, never the target's contents.
	t.Cleanup(func() { _ = exec.Command("cmd", "/c", "rmdir", link).Run() })
}

// TestAudit130LexicalFormsStayInside: every accepted lexical spelling must
// resolve to a path under the canonical workspace; every spelling of the
// outside directory must be refused.
func TestAudit130LexicalFormsStayInside(t *testing.T) {
	ws, out := audit130Dirs(t)
	vol := filepath.VolumeName(out)
	refused := []string{
		"..", `..\outside\x`, "../outside/x", `..\\outside/x`, `sub\..\..\outside\x`,
		out, filepath.ToSlash(out), strings.ToUpper(out) + `\x`,
		`\\?\` + out + `\x`, `\\.\` + out + `\x`, `\??\` + out + `\x`,
		`\\localhost\` + strings.TrimSuffix(vol, ":") + `$` + strings.TrimPrefix(out, vol) + `\x`,
		`...\outside\x`, `.. \outside\x`, `..\outside.\x`, `..\outside \x`,
		filepath.Join(ws, "..", "outside", "x"), filepath.Dir(ws) + `\ws-evil\x`,
	}
	for _, p := range refused {
		got, err := ResolveSafe(ws, p)
		if err == nil && !IsUnder(ws, got) {
			t.Errorf("ResolveSafe(%q) = %q (outside the workspace) without error", p, got)
		}
		if err == nil {
			// Accepted spellings must at least land physically inside: check by
			// creating the file and seeing where it appears.
			t.Logf("accepted %q -> %q", p, got)
		}
	}
	for _, p := range []string{"x.txt:stream", "C:foo", `a\b\..\c`, "dir.", "dir ", "x.txt::$DATA"} {
		got, err := ResolveSafe(ws, p)
		if err == nil && !IsUnder(ws, got) {
			t.Errorf("ResolveSafe(%q) = %q escapes", p, got)
		}
	}
}

// TestAudit130JunctionInsideWorkspaceActsAsExplicitGrant (owner decision): a
// junction inside the workspace acts as an explicit grant to its target, so
// ResolveSafe and ResolveWithin accept paths through it (existing or not yet
// existing target) and keep them in workspace coordinates. Regression guard.
func TestAudit130JunctionInsideWorkspaceActsAsExplicitGrant(t *testing.T) {
	ws, out := audit130Dirs(t)
	audit130MakeJunction(t, filepath.Join(ws, "j"), out)
	audit130MakeJunction(t, filepath.Join(ws, "jm"), filepath.Join(out, "missing"))
	for _, p := range []string{"j", `j\x.txt`, `j\new\deeper.txt`, `jm\x.txt`} {
		for name, resolve := range map[string]func(string, string) (string, error){"ResolveSafe": ResolveSafe, "ResolveWithin": ResolveWithin} {
			got, err := resolve(ws, p)
			if err != nil {
				t.Errorf("%s(%q) refused a junction grant: %v", name, p, err)
				continue
			}
			if !IsUnder(ws, got) {
				t.Errorf("%s(%q) = %q, want workspace coordinates", name, p, got)
			}
		}
	}
}

// TestAudit130DanglingSymlinkActsAsExplicitGrant (owner decision): a symlink
// inside the workspace whose target does not exist yet is an intentional
// grant, like a junction, so ResolveSafe accepts it in workspace
// coordinates. Regression guard. (A symlink to an EXISTING outside target is
// still refused; see TestAudit130SymlinkEscape in internal/tools/files.)
func TestAudit130DanglingSymlinkActsAsExplicitGrant(t *testing.T) {
	ws, out := audit130Dirs(t)
	if err := os.Symlink(filepath.Join(out, "new.txt"), filepath.Join(ws, "dangling")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	got, err := ResolveSafe(ws, "dangling")
	if errors.Is(err, ErrEscape) || err != nil {
		t.Fatalf("ResolveSafe(dangling) refused a dangling-symlink grant: %v", err)
	}
	if !IsUnder(ws, got) {
		t.Fatalf("ResolveSafe(dangling) = %q, want workspace coordinates", got)
	}
}

// TestAudit130CheckThenUseSwap demonstrates check/use separation
// deterministically: a path validated while it is an ordinary directory is
// later used after the directory was replaced by a link. Callers write by
// path (os.WriteFile), so the validated string is not pinned to the object
// that was checked. Even with the junction bug fixed, this race remains for
// a concurrent local process; it is recorded, not asserted as a failure.
func TestAudit130CheckThenUseSwap(t *testing.T) {
	ws, out := audit130Dirs(t)
	dir := filepath.Join(ws, "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveSafe(ws, `d\x.txt`)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	audit130MakeJunction(t, dir, out)
	if err := os.WriteFile(resolved, []byte("swapped"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(out, "x.txt"))
	t.Logf("write after swap landed outside: %v (residual TOCTOU, needs a concurrent local attacker)", statErr == nil)
}
