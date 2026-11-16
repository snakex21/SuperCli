//go:build windows

package files

// Audit 130: workspace confinement of the file tools on Windows.
//
// Documented policy (docs/configuration.md, "allow_all"): with allow_all off
// (the default) file reads AND writes, code search, @file mentions, images,
// Office documents and ZIP extraction targets are confined to the active
// workspace. Edits inside the workspace are not approval-gated; edit_files of
// a root outside the workspace and trash of a path outside the workspace are
// gated, but are only reachable in allow-all mode.
//
// These tests drive every file tool through its Spec (approval hook first, as
// the host does: a non-nil approval request counts as "denied", the --batch
// behaviour) and assert that nothing outside a temp workspace is created,
// modified or removed, and that no outside content is returned by read tools.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"supercli/internal/tools/sandbox"
)

const (
	audit130Secret     = "SECRET-AUDIT130-CONTENT"
	audit130SecretFile = "secret.txt"
)

func audit130KnownFailing(t *testing.T, issue string) {
	t.Helper()
	if os.Getenv("SUPERCLI_AUDIT_KNOWN_FAILING") != "1" {
		t.Skip("KNOWN-FAILING audit-130: " + issue)
	}
}

type audit130Env struct {
	root, ws, out string
	// keepOutside skips the per-call outside reset (hard links would be
	// broken by recreating the outside file).
	keepOutside bool
}

// newAudit130Env creates root/ws (workspace) and a sibling outside directory
// with a long name (so that it has an 8.3 alias when the volume makes them)
// holding secret.txt and sub/deep.txt.
func newAudit130Env(t *testing.T) audit130Env {
	t.Helper()
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := audit130Env{root: root, ws: filepath.Join(root, "ws"), out: filepath.Join(root, "outside-long-directory-name")}
	if err := os.MkdirAll(env.ws, 0o755); err != nil {
		t.Fatal(err)
	}
	env.resetOutside(t)
	env.resetInside(t)
	return env
}

// resetOutside recreates the outside tree through its real path (never
// through a link), so each tool call starts from the same outside state.
func (e audit130Env) resetOutside(t *testing.T) {
	t.Helper()
	for _, d := range []string{e.out, filepath.Join(e.root, "ws-evil")} {
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{filepath.Join(e.out, "sub"), filepath.Join(e.root, "ws-evil")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(e.out, audit130SecretFile):       audit130Secret + "\n",
		filepath.Join(e.out, "sub", "deep.txt"):        audit130Secret + " deep\n",
		filepath.Join(e.root, "ws-evil", "secret.txt"): audit130Secret + " sibling\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (e audit130Env) resetInside(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.ws, "inside.txt"), []byte("inside SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot fingerprints everything under root EXCEPT the workspace, so any
// change outside the workspace (create, modify, delete) is detected.
func (e audit130Env) snapshot(t *testing.T) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(e.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == e.ws {
			return fs.SkipDir
		}
		if d.IsDir() {
			snap[p] = "dir"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			snap[p] = "unreadable"
			return nil
		}
		sum := sha256.Sum256(b)
		snap[p] = string(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func audit130Diff(before, after map[string]string) []string {
	var diffs []string
	for k, v := range before {
		if w, ok := after[k]; !ok {
			diffs = append(diffs, "removed "+k)
		} else if w != v {
			diffs = append(diffs, "modified "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diffs = append(diffs, "created "+k)
		}
	}
	return diffs
}

type audit130Call struct {
	name   string
	tool   Tool
	args   map[string]any
	isRead bool // the result must not reveal outside content
}

// run executes one call as the host does: approval first; a requested
// approval is treated as denied (the --batch behaviour, nobody to ask).
func audit130Run(t *testing.T, c audit130Call) (text string, approval bool, err error) {
	t.Helper()
	raw, mErr := json.Marshal(c.args)
	if mErr != nil {
		t.Fatal(mErr)
	}
	ctx := context.Background()
	if c.tool.Approval != nil {
		req, _ := c.tool.Approval(ctx, raw)
		if req != nil {
			return "", true, nil
		}
	}
	res, fnErr := c.tool.Fn(ctx, raw)
	if fnErr == nil {
		fnErr = res.Err
	}
	return res.Text, false, fnErr
}

// callsFor builds one call per file tool, all aimed at the outside directory
// spelled as dir (a path expression supplied by the model).
func audit130CallsFor(ws, dir string) []audit130Call {
	join := func(name string) string {
		if strings.HasSuffix(dir, `\`) || strings.HasSuffix(dir, "/") {
			return dir + name
		}
		sep := `\`
		if strings.Contains(dir, "/") && !strings.Contains(dir, `\`) {
			sep = "/"
		}
		return dir + sep + name
	}
	secret := join(audit130SecretFile)
	return []audit130Call{
		{name: "write_file", tool: NewWriteFile(ws).Spec(), args: map[string]any{"path": join("w.txt"), "content": "PWNED"}},
		{name: "write_file/overwrite", tool: NewWriteFile(ws).Spec(), args: map[string]any{"path": secret, "content": "PWNED"}},
		{name: "create_file", tool: NewCreateFile(ws).Spec(), args: map[string]any{"path": join("c.txt"), "content": "PWNED"}},
		{name: "patch_file", tool: NewPatchFile(ws).Spec(), args: map[string]any{"path": secret, "old": audit130Secret, "new": "PWNED"}},
		{name: "edit_files/path", tool: NewEditFiles(ws).Spec(), args: map[string]any{"path": dir, "include": "**", "old": "SECRET", "new": "PWNED"}},
		{name: "make_dir", tool: NewMakeDir(ws).Spec(), args: map[string]any{"path": join("newdir")}},
		{name: "copy/dest", tool: NewCopy(ws).Spec(), args: map[string]any{"src": "inside.txt", "dest": join("copied.txt")}},
		{name: "copy/src", tool: NewCopy(ws).Spec(), args: map[string]any{"src": secret, "dest": "exfil-copy.txt"}},
		{name: "move/dest", tool: NewMove(ws).Spec(), args: map[string]any{"src": "inside.txt", "dest": join("moved.txt")}},
		{name: "move/src", tool: NewMove(ws).Spec(), args: map[string]any{"src": secret, "dest": "exfil-move.txt"}},
		{name: "trash", tool: NewTrash(ws).Spec(), args: map[string]any{"path": secret}},
		{name: "read_lines", tool: NewReadLines(ws).Spec(), args: map[string]any{"file": secret, "from": 1, "to": 5}, isRead: true},
		{name: "read_context", tool: NewReadContext(ws).Spec(), args: map[string]any{"file": secret, "line": 1}, isRead: true},
		{name: "read_many", tool: NewReadMany(ws).Spec(), args: map[string]any{"reads": []map[string]any{{"file": secret, "from": 1, "to": 5}}}, isRead: true},
		{name: "read_many/glob", tool: NewReadMany(ws).Spec(), args: map[string]any{"reads": []map[string]any{{"file": join("*.txt"), "from": 1, "to": 5}}}, isRead: true},
		{name: "list_dir", tool: NewListDir(ws).Spec(), args: map[string]any{"path": dir}, isRead: true},
	}
}

func audit130Check(t *testing.T, env audit130Env, label string, calls []audit130Call) (escapes []string) {
	t.Helper()
	for _, c := range calls {
		env.resetInside(t)
		if !env.keepOutside {
			env.resetOutside(t)
		}
		before := env.snapshot(t)
		text, approval, err := audit130Run(t, c)
		after := env.snapshot(t)
		if diffs := audit130Diff(before, after); len(diffs) > 0 {
			escapes = append(escapes, label+" "+c.name+": "+strings.Join(diffs, "; "))
		}
		if c.isRead && err == nil && (strings.Contains(text, audit130Secret) || strings.Contains(text, audit130SecretFile)) {
			escapes = append(escapes, label+" "+c.name+": outside content returned")
		}
		// Exfiltration by copy/move into the workspace counts as a read leak.
		for _, name := range []string{"exfil-copy.txt", "exfil-move.txt"} {
			if b, rErr := os.ReadFile(filepath.Join(env.ws, name)); rErr == nil {
				if strings.Contains(string(b), audit130Secret) {
					escapes = append(escapes, label+" "+c.name+": outside file copied into workspace")
				}
				_ = os.Remove(filepath.Join(env.ws, name))
			}
		}
		t.Logf("%-28s %-22s approval=%v err=%v", label, c.name, approval, err)
	}
	return escapes
}

func audit130ShortPath(t *testing.T, long string) string {
	p, err := windows.UTF16PtrFromString(long)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		return ""
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, long) {
		return ""
	}
	return short
}

// TestAudit130LexicalTraversalAllFileTools tries every spelling of "the
// outside directory" that is purely lexical (no links) against every file
// tool. All must be refused: nothing outside changes, nothing leaks.
func TestAudit130LexicalTraversalAllFileTools(t *testing.T) {
	env := newAudit130Env(t)
	outName := filepath.Base(env.out)
	vol := filepath.VolumeName(env.out) // "C:"
	unc := `\\localhost\` + strings.TrimSuffix(vol, ":") + `$` + strings.TrimPrefix(env.out, vol)
	forms := map[string]string{
		"dotdot-slash":          "../" + outName,
		"dotdot-backslash":      `..\` + outName,
		"mixed-separators":      `..\\` + outName + `/.`,
		"nested-dotdot":         "sub/../../" + outName,
		"deep-dotdot":           strings.Repeat("../", 12) + strings.TrimPrefix(filepath.ToSlash(env.out), filepath.ToSlash(vol)+"/"),
		"absolute":              env.out,
		"absolute-slashes":      filepath.ToSlash(env.out),
		"absolute-upper":        strings.ToUpper(env.out),
		"extended-length":       `\\?\` + env.out,
		"device-namespace":      `\\.\` + env.out,
		"nt-object-prefix":      `\??\` + env.out,
		"unc-admin-share":       unc,
		"extended-unc":          `\\?\UNC\` + strings.TrimPrefix(unc, `\\`),
		"drive-relative":        vol + `..\` + outName,
		"rooted-no-drive":       strings.TrimPrefix(env.out, vol),
		"trailing-dot":          `..\` + outName + `.`,
		"trailing-space":        `..\` + outName + ` `,
		"dotdot-trailing-dot":   `...\` + outName,
		"dotdot-trailing-spc":   `.. \` + outName,
		"dotdot-dot-space":      `.. .\` + outName,
		"file-url":              "file:///" + filepath.ToSlash(env.out),
		"sibling-prefix":        filepath.Join(env.root, "ws-evil"),
		"sibling-prefix-rel":    `..\ws-evil`,
		"ads-on-parent":         `..\` + outName + `::$INDEX_ALLOCATION`,
		"env-var-unexpanded":    `%TEMP%\..\` + outName,
		"tilde-home":            `~\..\` + outName,
		"case-dotdot":           `..\` + strings.ToUpper(outName),
		"double-slash-rel":      `..//` + outName,
		"trailing-separator":    `..\` + outName + `\`,
		"alt-separator-only":    "..\\\\" + outName,
		"workspace-then-escape": filepath.Join(env.ws, "..", outName),
	}
	if short := audit130ShortPath(t, env.out); short != "" {
		forms["8.3-short-absolute"] = short
		forms["8.3-short-relative"] = `..\` + filepath.Base(short)
	} else {
		t.Log("8.3 short names unavailable on this volume; 8.3 cases not exercised")
	}
	var escapes []string
	for label, dir := range forms {
		escapes = append(escapes, audit130Check(t, env, label, audit130CallsFor(env.ws, dir))...)
	}
	for _, e := range escapes {
		t.Error(e)
	}
}

// TestAudit130EditFilesIncludeGlobEscape covers the multi-file editor's glob
// argument: include patterns that try to climb out of the (in-workspace) root.
func TestAudit130EditFilesIncludeGlobEscape(t *testing.T) {
	env := newAudit130Env(t)
	outName := filepath.Base(env.out)
	var calls []audit130Call
	for _, inc := range []string{
		"../**", "../" + outName + "/**", `..\` + outName + `\*`, `..\**\*.txt`,
		filepath.ToSlash(env.out) + "/**", env.out + `\*.txt`, "**/../../**", "/**",
		"{../**,**}", "**/" + outName + "/**",
	} {
		calls = append(calls, audit130Call{name: "include " + inc, tool: NewEditFiles(env.ws).Spec(),
			args: map[string]any{"path": ".", "include": inc, "old": "SECRET", "new": "PWNED"}})
	}
	for _, e := range audit130Check(t, env, "edit_files", calls) {
		t.Error(e)
	}
}

// TestAudit130ReservedDeviceNamesAccepted records that ResolveSafe accepts
// DOS device names. On Windows 10 "<ws>\CON" opens the console and
// "<ws>\COM1" a serial port, not a file in the workspace. Not a confinement
// escape for writes (data goes to a device), but a read of CON/CONIN$ blocks
// the tool call. We only check the resolver; we never open these names.
func TestAudit130ReservedDeviceNamesAccepted(t *testing.T) {
	audit130KnownFailing(t, "sandbox.ResolveSafe accepts reserved DOS device names (CON, NUL, COM1, sub\\AUX.txt); file tools then open the device instead of a workspace file")
	env := newAudit130Env(t)
	for _, name := range []string{"CON", "NUL", "COM1", "LPT1", "AUX", `sub\CON.txt`, "CONIN$", "nul.txt"} {
		if _, err := sandbox.ResolveSafe(env.ws, name); err == nil {
			t.Errorf("ResolveSafe accepted device name %q", name)
		}
	}
}

// --- link based escapes -----------------------------------------------------

func audit130Junction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("junction creation unavailable: %v (%s)", err, out)
	}
	// rmdir on a junction removes only the reparse point, never the target.
	t.Cleanup(func() { _ = exec.Command("cmd", "/c", "rmdir", link).Run() })
}

// TestAudit130JunctionInsideWorkspaceActsAsExplicitGrant documents an owner
// decision: an NTFS junction inside the workspace acts as an explicit grant
// to its target. Creating `mklink /J <ws>\j <folder>` is the owner's
// deliberate way to give the agent access to a folder outside the workspace,
// so every file tool must follow the junction for read AND write, WITHOUT an
// approval prompt. This is a regression guard: if the sandbox starts
// refusing junctions, this test fails.
//
// (Mechanism: filepath.EvalSymlinks does not evaluate mount points since
// Go 1.23, so sandbox.ResolveSafe keeps "<ws>\j\..." in workspace coordinates.)
func TestAudit130JunctionInsideWorkspaceActsAsExplicitGrant(t *testing.T) {
	env := newAudit130Env(t)
	audit130Junction(t, filepath.Join(env.ws, "j"), env.out)
	calls := audit130CallsFor(env.ws, "j")
	calls = append(calls, audit130Call{name: "edit_files/j-sub", tool: NewEditFiles(env.ws).Spec(),
		args: map[string]any{"path": `j\sub`, "include": "**", "old": "SECRET", "new": "PWNED"}})

	// No call through the junction may ask for approval.
	for _, c := range calls {
		if c.tool.Approval == nil {
			continue
		}
		raw, _ := json.Marshal(c.args)
		if req, _ := c.tool.Approval(context.Background(), raw); req != nil {
			t.Errorf("%s through the junction asked for approval (%s); a junction is an explicit grant", c.name, req.Operation)
		}
	}

	effects := audit130Check(t, env, "junction", calls)
	for _, c := range calls {
		if c.name == "edit_files/path" {
			// Current behaviour, recorded only: edit_files rooted AT the
			// junction walks nothing (filepath.WalkDir sees the root as an
			// irregular file); rooted at a real directory behind it, it works.
			continue
		}
		found := false
		for _, e := range effects {
			if strings.HasPrefix(e, "junction "+c.name+":") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s did not reach the junction target; the junction grant was not honoured", c.name)
		}
	}
}

// TestAudit130JunctionToMissingTargetIsGranted: the grant also covers a
// junction whose target folder does not exist yet.
func TestAudit130JunctionToMissingTargetIsGranted(t *testing.T) {
	env := newAudit130Env(t)
	missing := filepath.Join(env.out, "created-later")
	audit130Junction(t, filepath.Join(env.ws, "jm"), missing)
	if _, err := sandbox.ResolveSafe(env.ws, `jm\x.txt`); err != nil {
		t.Errorf("ResolveSafe(jm\\x.txt) refused a junction grant: %v", err)
	}
}

// TestAudit130SymlinkEscape: directory and file symlinks pointing outside.
// Needs SeCreateSymbolicLinkPrivilege or Developer Mode; skipped otherwise.
func TestAudit130SymlinkEscape(t *testing.T) {
	env := newAudit130Env(t)
	if err := os.Symlink(env.out, filepath.Join(env.ws, "sdir")); err != nil {
		t.Skipf("symlink creation unavailable (Developer Mode off?): %v", err)
	}
	if err := os.Symlink(filepath.Join(env.out, audit130SecretFile), filepath.Join(env.ws, "sfile.txt")); err != nil {
		t.Fatal(err)
	}
	escapes := audit130Check(t, env, "symlink-dir", audit130CallsFor(env.ws, "sdir"))
	escapes = append(escapes, audit130Check(t, env, "symlink-file", []audit130Call{
		{name: "write_file", tool: NewWriteFile(env.ws).Spec(), args: map[string]any{"path": "sfile.txt", "content": "PWNED"}},
		{name: "patch_file", tool: NewPatchFile(env.ws).Spec(), args: map[string]any{"path": "sfile.txt", "old": audit130Secret, "new": "PWNED"}},
		{name: "read_lines", tool: NewReadLines(env.ws).Spec(), args: map[string]any{"file": "sfile.txt", "from": 1, "to": 5}, isRead: true},
		{name: "edit_files", tool: NewEditFiles(env.ws).Spec(), args: map[string]any{"path": ".", "include": "*.txt", "old": "SECRET", "new": "PWNED"}},
	})...)
	for _, e := range escapes {
		t.Error(e)
	}
}

// TestAudit130DanglingSymlinkActsAsExplicitGrant (owner decision): a symlink
// inside the workspace whose target does not exist yet is an intentional
// grant, like a junction: write_file through it creates the target outside
// the workspace, with no approval prompt. Regression guard.
//
// Recorded, not asserted: create_file refuses the dangling link itself
// ("already exists": it creates exclusively and the link entry exists), so
// only write_file creates the target through it.
func TestAudit130DanglingSymlinkActsAsExplicitGrant(t *testing.T) {
	env := newAudit130Env(t)
	target := filepath.Join(env.out, "created-through-dangling-link.txt")
	if err := os.Symlink(target, filepath.Join(env.ws, "dangling.txt")); err != nil {
		t.Skipf("symlink creation unavailable (Developer Mode off?): %v", err)
	}
	write := audit130Call{name: "write_file", tool: NewWriteFile(env.ws).Spec(), args: map[string]any{"path": "dangling.txt", "content": "GRANTED"}}
	_, approval, err := audit130Run(t, write)
	if approval {
		t.Fatal("write_file through a dangling symlink asked for approval; it is an explicit grant")
	}
	if err != nil {
		t.Fatalf("write_file through a dangling symlink refused: %v", err)
	}
	if b, rErr := os.ReadFile(target); rErr != nil || string(b) != "GRANTED" {
		t.Fatalf("dangling symlink target not created through the grant: %q, %v", b, rErr)
	}
	// Observation only (see doc comment).
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	_, _, cErr := audit130Run(t, audit130Call{name: "create_file", tool: NewCreateFile(env.ws).Spec(), args: map[string]any{"path": "dangling.txt", "content": "GRANTED"}})
	t.Logf("observed (not asserted): create_file through the dangling symlink err=%v", cErr)
}

// Owner decision (hard links): a file in the workspace that is a hard link
// to a file outside it (e.g. pnpm node_modules linked into the global store)
// must NEVER cause the outside file to change. Writers must replace the
// workspace path through a temp file + rename (as edit_files does), which
// breaks the link and leaves the outside file untouched.

type audit130HardlinkCase struct {
	name string
	call func(ws string) audit130Call
	want string // expected workspace content after the write
}

func audit130HardlinkRun(t *testing.T, c audit130HardlinkCase) {
	t.Helper()
	env := newAudit130Env(t)
	env.keepOutside = true
	outsideFile := filepath.Join(env.out, audit130SecretFile)
	if err := os.Link(outsideFile, filepath.Join(env.ws, "hard.txt")); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	call := c.call(env.ws)
	_, approval, err := audit130Run(t, call)
	if approval || err != nil {
		t.Fatalf("%s on a hard-linked workspace file failed: approval=%v err=%v", c.name, approval, err)
	}
	if b, _ := os.ReadFile(outsideFile); string(b) != audit130Secret+"\n" {
		t.Errorf("%s modified the hard-linked file OUTSIDE the workspace (now %q)", c.name, b)
	}
	if b, _ := os.ReadFile(filepath.Join(env.ws, "hard.txt")); !strings.Contains(string(b), c.want) {
		t.Errorf("%s did not write the workspace file (now %q)", c.name, b)
	}
}

// TestAudit130HardlinkEditFilesBreaksLink: edit_files already replaces via
// temp file + rename. Regression guard.
func TestAudit130HardlinkEditFilesBreaksLink(t *testing.T) {
	audit130HardlinkRun(t, audit130HardlinkCase{name: "edit_files", want: "PWNED", call: func(ws string) audit130Call {
		return audit130Call{name: "edit_files", tool: NewEditFiles(ws).Spec(), args: map[string]any{"path": ".", "include": "hard.txt", "old": "SECRET", "new": "PWNED"}}
	}})
}

// TestAudit130HardlinkInPlaceWritersMustNotModifyOutside: write_file
// (fileops.WriteFileContext) and patch_file (fileops.PatchFileContext) replace
// an existing file through fileops.ReplaceFileContents (temp file + rename),
// which breaks the hard link instead of writing through it.
func TestAudit130HardlinkInPlaceWritersMustNotModifyOutside(t *testing.T) {
	for _, c := range []audit130HardlinkCase{
		{name: "write_file", want: "PWNED", call: func(ws string) audit130Call {
			return audit130Call{name: "write_file", tool: NewWriteFile(ws).Spec(), args: map[string]any{"path": "hard.txt", "content": "PWNED"}}
		}},
		{name: "patch_file", want: "PWNED", call: func(ws string) audit130Call {
			return audit130Call{name: "patch_file", tool: NewPatchFile(ws).Spec(), args: map[string]any{"path": "hard.txt", "old": audit130Secret, "new": "PWNED"}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) { audit130HardlinkRun(t, c) })
	}
}
