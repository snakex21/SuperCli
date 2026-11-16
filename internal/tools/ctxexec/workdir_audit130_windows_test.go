//go:build windows

package ctxexec

// Audit 130: ctx_execute working-directory confinement. The shell command
// itself is NOT confined by design (docs describe no shell sandbox; only the
// workdir is resolved through sandbox.ResolveSafe), so these tests check the
// workdir only.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

func audit130Workspace(t *testing.T) (ws, out string) {
	t.Helper()
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
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

func TestAudit130WorkdirTraversalRefused(t *testing.T) {
	ws, out := audit130Workspace(t)
	for _, wd := range []string{"..", `..\outside`, "../outside", out, filepath.ToSlash(out), `\\?\` + out, `...\outside`, `.. \outside`} {
		res, err := New(ws).Run(context.Background(), &Request{Command: []string{"cmd", "/c", "cd"}, Workdir: wd})
		if err == nil && res != nil && strings.Contains(strings.ToLower(res.Stdout), strings.ToLower(out)) {
			t.Errorf("workdir %q ran in %s", wd, strings.TrimSpace(res.Stdout))
		}
	}
}

// TestAudit130WorkdirJunctionActsAsExplicitGrant (owner decision): a junction
// inside the workspace acts as an explicit grant to its target, so a
// ctx_execute workdir through it runs in the target. Regression guard.
func TestAudit130WorkdirJunctionActsAsExplicitGrant(t *testing.T) {
	ws, out := audit130Workspace(t)
	link := filepath.Join(ws, "j")
	if o, err := exec.Command("cmd", "/c", "mklink", "/J", link, out).CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, o)
	}
	t.Cleanup(func() { _ = exec.Command("cmd", "/c", "rmdir", link).Run() })
	res, err := New(ws).Run(context.Background(), &Request{Command: []string{"cmd", "/c", "echo x> written-by-shell.txt"}, Workdir: "j"})
	if err != nil {
		t.Fatalf("workdir through the junction grant refused: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "written-by-shell.txt")); statErr != nil {
		t.Errorf("command with workdir j did not run in the junction target %s (result workdir %q)", out, res.Workdir)
	}
}
