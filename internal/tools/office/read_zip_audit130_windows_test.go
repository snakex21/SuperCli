//go:build windows

package office

// Audit 130: read_zip action=extract.
//
// Owner decisions: an NTFS junction inside the workspace is an intentional
// grant to its target, so extracting INTO or THROUGH such a junction must be
// allowed; every zip-slip protection on ENTRY NAMES must stay in force.

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

type audit130ZipEnv struct {
	root, ws, out string
}

func newAudit130ZipEnv(t *testing.T, withJunction bool) audit130ZipEnv {
	t.Helper()
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := audit130ZipEnv{root: root, ws: filepath.Join(root, "ws"), out: filepath.Join(root, "outside")}
	for _, d := range []string{e.ws, filepath.Join(e.out, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if withJunction {
		link := filepath.Join(e.ws, "j")
		if o, err := exec.Command("cmd", "/c", "mklink", "/J", link, e.out).CombinedOutput(); err != nil {
			t.Skipf("junction creation unavailable: %v (%s)", err, o)
		}
		// rmdir removes only the reparse point, never the target.
		t.Cleanup(func() { _ = exec.Command("cmd", "/c", "rmdir", link).Run() })
	}
	return e
}

func (e audit130ZipEnv) writeZip(t *testing.T, name string, entries ...string) {
	t.Helper()
	f, err := os.Create(filepath.Join(e.ws, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, en := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: en, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("ZIPDATA"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// filesUnder lists regular files under dir (real path, not through links).
func audit130FilesUnder(dir string) map[string]bool {
	got := map[string]bool{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			got[p] = true
		}
		return nil
	})
	return got
}

func audit130Extract(t *testing.T, ws string, args map[string]any) error {
	t.Helper()
	raw, _ := json.Marshal(args)
	res, err := NewReadZip(ws, 1<<20).Spec().Fn(context.Background(), raw)
	if err == nil {
		err = res.Err
	}
	return err
}

// TestAudit130ReadZipSlipEntriesRefused: every zip-slip entry name is refused
// one by one (one archive per entry), into a plain target and into a junction
// target, and nothing appears outside the workspace and outside the granted
// junction target. Also: a target_dir spelled outside the workspace is
// refused. Regression guard.
func TestAudit130ReadZipSlipEntriesRefused(t *testing.T) {
	e := newAudit130ZipEnv(t, true)
	slips := []string{
		"../a.txt", `..\b.txt`, "x/../../c.txt", "/d.txt", `\e.txt`,
		filepath.ToSlash(e.out) + "/f.txt", e.out + `\g.txt`, `C:..\h.txt`, `C:\i.txt`,
		"x/../../../j.txt", "../outside/k.txt", "../../l.txt", `\\?\` + e.out + `\m.txt`,
		"CON", "x/NUL.txt",
	}
	before := audit130FilesUnder(e.root)
	for i, entry := range slips {
		name := "slip" + strings.Repeat("_", i) + ".zip"
		e.writeZip(t, name, entry)
		for _, target := range []string{"x", `j\sub`} {
			if err := audit130Extract(t, e.ws, map[string]any{"path": name, "action": "extract", "target_dir": target}); err == nil {
				t.Errorf("entry %q into target %q was extracted; want refusal", entry, target)
			}
		}
		_ = os.Remove(filepath.Join(e.ws, name))
	}
	for p := range audit130FilesUnder(e.root) {
		if !before[p] && !strings.HasPrefix(p, e.ws+string(filepath.Separator)) {
			t.Errorf("zip-slip wrote %s outside the workspace", p)
		}
	}
	e.writeZip(t, "plain.zip", "ok.txt")
	for _, target := range []string{"../outside", e.out, filepath.ToSlash(e.out), `..\outside\sub`} {
		if err := audit130Extract(t, e.ws, map[string]any{"path": "plain.zip", "action": "extract", "target_dir": target}); err == nil {
			t.Errorf("target_dir %q outside the workspace was accepted", target)
		}
	}
	if _, err := os.Stat(filepath.Join(e.out, "ok.txt")); err == nil {
		t.Error("ok.txt was extracted directly into the outside directory")
	}
}

// TestAudit130ReadZipExtractThroughJunctionGranted: extraction into a
// junction (target_dir "j", "j\sub") and through one (entry "j/x.txt" into
// the workspace root) must succeed and land in the junction target.
// rejectZipUnresolvedLinks lets directory junctions pass (still refusing
// unresolved symlinks and other reparse points), and sandbox resolves a
// target below a junction.
func TestAudit130ReadZipExtractThroughJunctionGranted(t *testing.T) {
	e := newAudit130ZipEnv(t, true)
	e.writeZip(t, "into.zip", "into.txt")
	e.writeZip(t, "through.zip", "j/through.txt")
	cases := []struct {
		zip, target, want string
	}{
		{"into.zip", "j", filepath.Join(e.out, "into.txt")},
		{"into.zip", `j\sub`, filepath.Join(e.out, "sub", "into.txt")},
		{"through.zip", ".", filepath.Join(e.out, "through.txt")},
	}
	for _, c := range cases {
		if err := audit130Extract(t, e.ws, map[string]any{"path": c.zip, "action": "extract", "target_dir": c.target}); err != nil {
			t.Errorf("extract %s into %q refused: %v", c.zip, c.target, err)
			continue
		}
		if b, err := os.ReadFile(c.want); err != nil || string(b) != "ZIPDATA" {
			t.Errorf("extract %s into %q: %s not written through the junction grant (%v)", c.zip, c.target, c.want, err)
		}
	}
}
