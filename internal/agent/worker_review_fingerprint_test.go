package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Review 3: in git the fingerprint must change when an already modified
// file is edited again (git status alone stays " M file") and when an
// untracked file changes.
func TestWorkspaceFingerprintSeesContentChangesInGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("a.go", "package a\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	write("a.go", "package a\n// one\n")
	ctx := context.Background()
	before := workspaceFingerprint(ctx, dir)
	write("a.go", "package a\n// two\n")
	if after := workspaceFingerprint(ctx, dir); after == before {
		t.Fatal("a second edit of a modified file left the fingerprint unchanged")
	}
	run("add", "a.go")
	staged := workspaceFingerprint(ctx, dir)
	write("a.go", "package a\n// three\n")
	run("add", "a.go")
	if workspaceFingerprint(ctx, dir) == staged {
		t.Fatal("a change to the staged diff left the fingerprint unchanged")
	}
	write("new.txt", "x")
	untracked := workspaceFingerprint(ctx, dir)
	write("new.txt", "xyz")
	if workspaceFingerprint(ctx, dir) == untracked {
		t.Fatal("an edit of an untracked file left the fingerprint unchanged")
	}
	if workspaceFingerprint(ctx, dir) != workspaceFingerprint(ctx, dir) {
		t.Fatal("fingerprint is not stable")
	}
}

// Outside git a walk beyond its bound claims nothing: "not checked", never
// "unchanged".
func TestWorkspaceFingerprintBeyondBoundIsUnchecked(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := fingerprintMaxFiles
	fingerprintMaxFiles = 2
	defer func() { fingerprintMaxFiles = old }()
	if got := workspaceFingerprint(context.Background(), dir); got != fingerprintUnchecked {
		t.Fatalf("fingerprint over the bound = %q, want %q", got, fingerprintUnchecked)
	}
	fingerprintMaxFiles = 10
	if got := workspaceFingerprint(context.Background(), dir); got == fingerprintUnchecked || got == "" {
		t.Fatalf("fingerprint under the bound = %q", got)
	}
}
