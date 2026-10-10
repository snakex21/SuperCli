package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newNarrowRecordTestManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, filepath.Join(root, "data"))
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func writeNarrowRecordFixture(t *testing.T, m *Manager, path string, body []byte) snapshotFile {
	t.Helper()
	full := filepath.Join(m.home, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	return snapshotFile{path: path, info: info}
}

func narrowRecordFixtureTree(t *testing.T, m *Manager, entries string) string {
	t.Helper()
	cmd := m.gitCommand(context.Background(), "mktree", "-z")
	cmd.Stdin = strings.NewReader(entries)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture mktree: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func narrowRecordTreeEntries(t *testing.T, m *Manager, commit string) map[string]string {
	t.Helper()
	out, err := m.git(context.Background(), "ls-tree", "--full-tree", "-r", "-z", commit)
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			continue
		}
		metadata, path, found := strings.Cut(line, "\t")
		if !found {
			t.Fatalf("invalid fixture ls-tree entry: %q", line)
		}
		entries[path] = metadata
	}
	return entries
}

func assertNarrowRecordReadback(t *testing.T, m *Manager, commit string, original map[string]string, bodies map[string][]byte) {
	t.Helper()
	want := make(map[string]string, len(bodies))
	for path := range bodies {
		want[path] = original[path]
	}
	got := narrowRecordTreeEntries(t, m, commit)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("narrowed OIDs/modes/paths differ: got=%v want=%v", got, want)
	}
	for path, body := range bodies {
		fields := strings.Fields(got[path])
		out, err := m.git(context.Background(), "cat-file", "blob", fields[2])
		if err != nil || !bytes.Equal([]byte(out), body) {
			t.Fatalf("raw blob %q changed: err=%v", path, err)
		}
	}
	out, err := m.git(context.Background(), "cat-file", "commit", commit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\nparent ") || !strings.Contains(out, "\nauthor SuperCli <checkpoint@local> 0 +0000\ncommitter SuperCli <checkpoint@local> 0 +0000\n\nSuperCli checkpoint\n") {
		t.Fatalf("narrowed commit lost deterministic parentless identity: %q", out)
	}
}

func assertNoNarrowRecordTemporaries(t *testing.T, m *Manager) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(m.repo, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "checkpoint-narrow-") {
			t.Fatalf("leaked narrow temporary: %s", entry.Name())
		}
	}
}

func TestNarrowChangedSnapshotsPreservesOnlyRecordFiles(t *testing.T) {
	ctx := context.Background()
	m := newNarrowRecordTestManager(t)
	if err := m.ensureRepoLocked(ctx); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"filter.forbidden.clean", "supercli-fixture-filter-must-never-run"}, {"filter.forbidden.required", "true"}} {
		if _, err := m.git(ctx, "config", pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	writeNarrowRecordFixture(t, m, ".gitattributes", []byte("*.txt text eol=lf filter=forbidden\n"))
	writeNarrowRecordFixture(t, m, "unrelated-large.bin", bytes.Repeat([]byte{0, 255, 128, 1}, 256<<10))
	renameBody := []byte{0, 255, 0, 3, 128}
	beforeBodies := map[string][]byte{
		"modify.txt":   []byte("before\r\nraw CRLF\r\n"),
		"deleted.bin":  {0, 255, 1, 0},
		"old/name.bin": renameBody,
	}
	for path, body := range beforeBodies {
		writeNarrowRecordFixture(t, m, path, body)
	}
	before, err := m.captureSnapshot(ctx, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	afterBodies := map[string][]byte{
		"modify.txt":   []byte("after\r\nraw CRLF\r\n"),
		"created.bin":  {0, 128, 254, 0, 7},
		"new/name.bin": renameBody,
	}
	if err := os.Remove(filepath.Join(m.home, "deleted.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(m.home, "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(m.home, "old", "name.bin"), filepath.Join(m.home, "new", "name.bin")); err != nil {
		t.Fatal(err)
	}
	for path, body := range afterBodies {
		if path != "new/name.bin" {
			writeNarrowRecordFixture(t, m, path, body)
		}
	}
	after, err := m.captureSnapshot(ctx, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := m.diffFiles(ctx, before, after)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"created.bin", "deleted.bin", "modify.txt", "new/name.bin", "old/name.bin"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("fixture changes=%v want=%v", files, wantFiles)
	}
	beforeEntries := narrowRecordTreeEntries(t, m, before)
	afterEntries := narrowRecordTreeEntries(t, m, after)
	refsBefore, err := m.git(ctx, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		t.Fatal(err)
	}
	metadata := []byte("[]\n")
	if err := os.WriteFile(m.meta, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	// A missing workspace proves narrowing cannot read attributes or source
	// files. All Git enumeration must use the private object database alone.
	hiddenHome := filepath.Join(filepath.Dir(m.home), "workspace-unavailable")
	if err := os.Rename(m.home, hiddenHome); err != nil {
		t.Fatal(err)
	}
	minimalBefore, minimalAfter, narrowErr := m.narrowChangedSnapshots(ctx, before, after, files)
	if err := os.Rename(hiddenHome, m.home); err != nil {
		t.Fatal(err)
	}
	if narrowErr != nil {
		t.Fatal(narrowErr)
	}
	assertNarrowRecordReadback(t, m, minimalBefore, beforeEntries, beforeBodies)
	assertNarrowRecordReadback(t, m, minimalAfter, afterEntries, afterBodies)
	refsAfter, err := m.git(ctx, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil || refsAfter != refsBefore {
		t.Fatalf("narrowing changed refs/latest: err=%v", err)
	}
	gotMetadata, err := os.ReadFile(m.meta)
	if err != nil || !bytes.Equal(gotMetadata, metadata) {
		t.Fatalf("narrowing changed record metadata: err=%v", err)
	}
	// Selection order and duplicates cannot affect canonical tree identities.
	reordered := []string{"old/name.bin", "new/name.bin", "modify.txt", "deleted.bin", "created.bin", "modify.txt"}
	gotBefore, gotAfter, err := m.narrowChangedSnapshots(ctx, before, after, reordered)
	if err != nil || gotBefore != minimalBefore || gotAfter != minimalAfter {
		t.Fatalf("non-deterministic selection: before=%s after=%s err=%v", gotBefore, gotAfter, err)
	}
	if out, err := m.git(ctx, "fsck", "--strict", "--no-reflogs", minimalBefore, minimalAfter); err != nil {
		t.Fatalf("Git rejected narrowed objects: %v: %s", err, out)
	}
	assertNoNarrowRecordTemporaries(t, m)
}

func TestNarrowChangedSnapshotsMatchesCanonicalGitTreeAndModes(t *testing.T) {
	ctx := context.Background()
	m := newNarrowRecordTestManager(t)
	if err := m.ensureRepoLocked(ctx); err != nil {
		t.Fatal(err)
	}
	file := writeNarrowRecordFixture(t, m, "fixture.bin", []byte{0, 255, 1})
	blob, err := m.storeRawBlob(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	childTree := narrowRecordFixtureTree(t, m, fmt.Sprintf("100644 blob %s\tchild.bin%c", blob, 0))
	// Git's directory ordering places foo.bar before foo/ before foo0.
	rootTree := narrowRecordFixtureTree(t, m, fmt.Sprintf("040000 tree %s\tfoo%c100755 blob %s\tfoo0%c100644 blob %s\tfoo.bar%c", childTree, 0, blob, 0, blob, 0))
	source, err := m.commitTreeLocked(ctx, rootTree)
	if err != nil {
		t.Fatal(err)
	}
	before, after, err := m.narrowChangedSnapshots(ctx, source, source, []string{"foo0", "foo/child.bin", "foo.bar"})
	if err != nil || before != source || after != source {
		t.Fatalf("Go tree/commit differs from canonical Git OID: before=%s after=%s source=%s err=%v", before, after, source, err)
	}
	entries := narrowRecordTreeEntries(t, m, before)
	if entries["foo0"] != "100755 blob "+blob || entries["foo.bar"] != "100644 blob "+blob {
		t.Fatalf("file modes changed: %v", entries)
	}
	if out, err := m.git(ctx, "fsck", "--strict", "--no-reflogs", before); err != nil {
		t.Fatalf("Git rejected canonical tree: %v: %s", err, out)
	}
	assertNoNarrowRecordTemporaries(t, m)
}

func TestNarrowChangedSnapshotsRejectsNonRegularSelectedMode(t *testing.T) {
	ctx := context.Background()
	m := newNarrowRecordTestManager(t)
	if err := m.ensureRepoLocked(ctx); err != nil {
		t.Fatal(err)
	}
	file := writeNarrowRecordFixture(t, m, "fixture.bin", []byte("link-target"))
	blob, err := m.storeRawBlob(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	tree := narrowRecordFixtureTree(t, m, fmt.Sprintf("120000 blob %s\tselected-link%c", blob, 0))
	source, err := m.commitTreeLocked(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}
	before, after, err := m.narrowChangedSnapshots(ctx, source, source, []string{"selected-link"})
	if err == nil || before != "" || after != "" || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("unsafe selected mode accepted: before=%s after=%s err=%v", before, after, err)
	}
	assertNoNarrowRecordTemporaries(t, m)
}

func TestNarrowChangedSnapshotSelectionBounds(t *testing.T) {
	for _, path := range []string{"", "../escape", "a/../b", "/absolute", "a//b", "a\\b", "a/\x00b", ".git/config"} {
		if _, err := narrowSelectedPaths([]string{path}); err == nil {
			t.Errorf("invalid selected path accepted: %q", path)
		}
	}
	if _, err := narrowSelectedPaths(make([]string, maxSnapshotFiles+1)); !errors.Is(err, ErrSnapshotLimit) {
		t.Fatalf("file count was not bounded: %v", err)
	}
	if _, err := narrowSelectedPaths([]string{strings.Repeat("a", maxNarrowEntryBytes+1)}); err == nil {
		t.Fatal("oversized selected path accepted")
	}
	for _, oid := range []string{"", "--all", strings.Repeat("0", 40), strings.Repeat("g", 40), strings.Repeat("a", 64)} {
		if validNarrowOID(oid) {
			t.Errorf("invalid snapshot OID accepted: %q", oid)
		}
	}
}
