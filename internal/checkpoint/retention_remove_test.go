package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func retentionGitFixtureInput(t *testing.T, m *Manager, input string, args ...string) string {
	t.Helper()
	cmd := retentionGitCommand(context.Background(), m.repo, args...)
	cmd.Stdin = strings.NewReader(input)
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("synthetic Git object creation: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func retentionOrphanGitObjects(t *testing.T, m *Manager) []string {
	t.Helper()
	blob := retentionGitFixtureInput(t, m, "native Git orphan bytes\x00\r\n", "hash-object", "-w", "--stdin")
	tree := retentionGitFixtureInput(t, m, "100644 blob "+blob+"\tsynthetic-orphan.bin\n", "mktree")
	commit := retentionGitFixtureInput(t, m, "synthetic unreachable checkpoint\n", "-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit-tree", tree)
	return []string{blob, tree, commit}
}

func retentionObjectPath(m *Manager, oid string) string {
	return filepath.Join(m.repo, "objects", oid[:2], oid[2:])
}

func retentionCandidate(t *testing.T, a *StoreRetentionAudit, path string) RetentionUnit {
	t.Helper()
	rel, err := filepath.Rel(a.dataDir, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range a.Inventory.Unreferenced {
		if unit.Path == filepath.ToSlash(rel) {
			return unit
		}
	}
	t.Fatalf("synthetic orphan not classified as unreachable: %s", rel)
	return RetentionUnit{}
}

func TestStoreRetentionRemovesNativeGitReadOnlyObjects(t *testing.T) {
	m, data, _, newest := retentionFixture(t)
	retentionTransaction(t, m, func() {
		oids := retentionOrphanGitObjects(t, m)
		for _, oid := range oids {
			info, err := os.Lstat(retentionObjectPath(m, oid))
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS == "windows" && info.Mode().Perm()&0222 != 0 {
				t.Fatal("native Git-created object did not carry Windows READONLY")
			}
		}
		a, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		units := make([]RetentionUnit, 0, len(oids))
		var expected int64
		for _, oid := range oids {
			u := retentionCandidate(t, a, retentionObjectPath(m, oid))
			units = append(units, u)
			expected += u.Bytes
		}
		deleted, err := deleteRetentionLooseLocked(context.Background(), a, units)
		if err != nil || deleted != expected {
			t.Fatalf("native loose reclaim=%d expected=%d err=%v", deleted, expected, err)
		}
		for _, oid := range oids {
			if _, err := os.Lstat(retentionObjectPath(m, oid)); !os.IsNotExist(err) {
				t.Fatalf("native loose file survived: %v", err)
			}
		}
		if _, err := m.git(context.Background(), "cat-file", "-e", newest.After); err != nil {
			t.Fatal("retained root was removed:", err)
		}
	})
}

func TestStoreRetentionHardLinkIsFixedAndNeverChanged(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		oid := retentionOrphanGitObjects(t, m)[0]
		path := retentionObjectPath(m, oid)
		external := filepath.Join(t.TempDir(), "other-name")
		if err := os.Link(path, external); err != nil {
			t.Skip("fixture filesystem has no hard links:", err)
		}
		before, err := os.ReadFile(external)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(external)
		if err != nil {
			t.Fatal(err)
		}
		a, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(data, path)
		unit := RetentionUnit{filepath.ToSlash(rel), info.Size()}
		fixed := false
		for _, u := range a.Inventory.Fixed {
			fixed = fixed || u.Path == unit.Path
		}
		if !fixed {
			t.Fatal("hard-linked object was not protected as fixed")
		}
		if deleted, err := deleteRetentionLooseLocked(context.Background(), a, []RetentionUnit{unit}); deleted != 0 || !errors.Is(err, ErrStoreInventory) {
			t.Fatalf("hard link permitted for deletion: %d %v", deleted, err)
		}
		after, err := os.ReadFile(external)
		current, statErr := os.Lstat(external)
		if err != nil || statErr != nil || !bytes.Equal(before, after) || info.Mode() != current.Mode() {
			t.Fatalf("external hard link changed: %v %v", err, statErr)
		}
	})
}

func TestStoreRetentionRejectsReplacedCandidateBeforeDeletion(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		path := retentionObjectPath(m, retentionOrphanGitObjects(t, m)[0])
		a, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		unit := retentionCandidate(t, a, path)
		old := a.files[unit.Path]
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, filepath.Join(t.TempDir(), "original-native-object")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old.ModTime(), old.ModTime()); err != nil {
			t.Fatal(err)
		}
		if deleted, err := deleteRetentionLooseLocked(context.Background(), a, []RetentionUnit{unit}); deleted != 0 || !errors.Is(err, ErrStoreInventory) {
			t.Fatalf("same-size/time substituted file was deleted: %d %v", deleted, err)
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, data) {
			t.Fatal("substituted file changed:", err)
		}
	})
}
