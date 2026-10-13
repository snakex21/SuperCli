package checkpoint

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func nestedFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	cmd := exec.Command("git", append([]string{"-c", "init.defaultRefFormat=files", "-c", "user.name=Checkpoint Test", "-c", "user.email=checkpoint@example.invalid", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = checkpointGitEnv()
	configureCheckpointCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Include Git metadata and newly created files, not just a known source file.
func nestedFixtureTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[filepath.ToSlash(rel)+"/"] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[filepath.ToSlash(rel)] = string(data)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNestedRepositoriesAreExcludedAndPreservedByRestore(t *testing.T) {
	t.Parallel()
	for _, scoped := range []bool{false, true} {
		for _, kind := range []string{"directory", "gitfile", "gitlink"} {
			name := "workspace/" + kind
			if scoped {
				name = "explicit-root/" + kind
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				home := t.TempDir()
				nested := filepath.Join(home, "container", "NestCafe")
				app := filepath.Join(home, "container", "a.txt")
				deleted := filepath.Join(home, "container", "deleted.txt")
				created := filepath.Join(home, "container", "created.txt")
				writeCheckpointFixture(t, app, "before")
				writeCheckpointFixture(t, deleted, "restore me")
				writeCheckpointFixture(t, filepath.Join(nested, "source.txt"), "nested before")
				nestedFixtureGit(t, home, "init")
				nestedFixtureGit(t, home, "add", "container/a.txt", "container/deleted.txt")
				if kind == "gitfile" {
					nestedFixtureGit(t, nested, "init", "--separate-git-dir="+t.TempDir())
				} else {
					nestedFixtureGit(t, nested, "init")
				}
				outerGit := nestedFixtureTree(t, filepath.Join(home, ".git"))
				m := openSnapshotTestManager(t, home)
				if kind == "gitlink" {
					nestedFixtureGit(t, nested, "add", "source.txt")
					nestedFixtureGit(t, nested, "commit", "-m", "fixture")
					head := nestedFixtureGit(t, nested, "rev-parse", "HEAD")
					if err := m.ensureRepoLocked(ctx); err != nil {
						t.Fatal(err)
					}
					// Exercise the slashless directory entry returned for a gitlink
					// by the checkpoint store's own ls-files index.
					if _, err := m.git(ctx, "update-index", "--add", "--cacheinfo", "160000,"+head+",container/NestCafe"); err != nil {
						t.Fatal(err)
					}
					found := false
					if err := m.eachWorkspacePath(ctx, func(path string) error {
						found = found || path == "container/NestCafe"
						return nil
					}); err != nil || !found {
						t.Fatalf("slashless gitlink not enumerated: found=%v err=%v", found, err)
					}
				}
				turn := m.NewTurn("nested", "edit ordinary files")
				toolName, args := "ctx_execute", `{}`
				if scoped {
					toolName, args = "read_zip", `{"action":"extract","target_dir":"container"}`
				}
				called := false
				tool := turn.Wrap(tools.Tool{Name: toolName, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					called = true
					writeCheckpointFixture(t, app, "after")
					writeCheckpointFixture(t, created, "created")
					writeCheckpointFixture(t, filepath.Join(nested, "source.txt"), "nested current")
					writeCheckpointFixture(t, filepath.Join(nested, "new.txt"), "not part of the checkpoint")
					return tools.Result{}, os.Remove(deleted)
				}})
				runSnapshotTool(t, tool, args)
				if !called {
					t.Fatal("checkpoint blocked the tool")
				}
				record, err := turn.Complete(ctx)
				want := []string{"container/a.txt", "container/created.txt", "container/deleted.txt"}
				if err != nil || record == nil || !reflect.DeepEqual(record.Files, want) {
					t.Fatalf("record=%+v err=%v", record, err)
				}
				for _, commit := range []string{turn.before, turn.snapshotAfter, record.Before, record.After} {
					paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", commit)
					if err != nil || strings.Contains(paths, "NestCafe") || !strings.Contains(paths, "container/a.txt") {
						t.Fatalf("incorrect snapshot: %q err=%v", paths, err)
					}
				}
				state := nestedFixtureTree(t, nested)
				if _, err := m.Undo(ctx, record.ID); err != nil {
					t.Fatal(err)
				}
				assertFileBytes(t, app, []byte("before"))
				assertFileBytes(t, deleted, []byte("restore me"))
				if _, err := os.Stat(created); !os.IsNotExist(err) {
					t.Fatalf("created ordinary file was not undone: %v", err)
				}
				if got := nestedFixtureTree(t, nested); !reflect.DeepEqual(got, state) {
					t.Fatal("undo changed the nested repository")
				}
				if _, err := m.Redo(ctx, record.ID); err != nil {
					t.Fatal(err)
				}
				assertFileBytes(t, app, []byte("after"))
				assertFileBytes(t, created, []byte("created"))
				if _, err := os.Stat(deleted); !os.IsNotExist(err) {
					t.Fatalf("deleted ordinary file was not redone: %v", err)
				}
				if got := nestedFixtureTree(t, nested); !reflect.DeepEqual(got, state) {
					t.Fatal("redo changed the nested repository")
				}
				if got := nestedFixtureTree(t, filepath.Join(home, ".git")); !reflect.DeepEqual(got, outerGit) {
					t.Fatal("checkpoint changed the workspace's own Git metadata")
				}
			})
		}
	}
}

func TestExplicitRootsInsideNestedRepositoryAreExcluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := t.TempDir()
	nested := filepath.Join(home, "Nested")
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "ordinary")
	writeCheckpointFixture(t, filepath.Join(nested, "deep", "a.txt"), "nested")
	nestedFixtureGit(t, home, "init")
	nestedFixtureGit(t, nested, "init")
	m := openSnapshotTestManager(t, home)
	for _, root := range []string{".", "Nested", "Nested/deep", "Nested/deep/a.txt"} {
		t.Run(root, func(t *testing.T) {
			commit, err := m.captureSnapshot(ctx, []string{root}, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", commit)
			want := ""
			if root == "." {
				want = "a.txt\n"
			}
			if err != nil || paths != want {
				t.Fatalf("snapshot=%q want=%q err=%v", paths, want, err)
			}
		})
	}
}

func TestWorkspaceSnapshotExcludesLegacyTrackedNestedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, "a.txt"), "ordinary")
	writeCheckpointFixture(t, filepath.Join(home, "Nested", "source.txt"), "source")
	m := openSnapshotTestManager(t, home)
	if _, err := m.capture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.git(ctx, "add", "--", "Nested/source.txt"); err != nil {
		t.Fatal(err)
	}
	nestedFixtureGit(t, filepath.Join(home, "Nested"), "init")
	commit, err := m.capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", commit)
	if err != nil || paths != "a.txt\n" {
		t.Fatalf("legacy nested index entry captured: %q err=%v", paths, err)
	}
}

func TestRestoreRefusesDirectoryThatBecameNestedRepository(t *testing.T) {
	t.Parallel()
	for _, redo := range []bool{false, true} {
		t.Run(map[bool]string{false: "undo", true: "redo"}[redo], func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			nested := filepath.Join(home, "Nested")
			app := filepath.Join(home, "a.txt")
			file := filepath.Join(nested, "source.txt")
			created := filepath.Join(nested, "created.txt")
			writeCheckpointFixture(t, app, "before")
			writeCheckpointFixture(t, file, "before")
			m := openSnapshotTestManager(t, home)
			turn := m.NewTurn("later-repo", "edit ordinary directory")
			if err := turn.ensureBefore(ctx); err != nil {
				t.Fatal(err)
			}
			writeCheckpointFixture(t, app, "after")
			writeCheckpointFixture(t, file, "after")
			writeCheckpointFixture(t, created, "after")
			record, err := turn.Complete(ctx)
			if err != nil || record == nil {
				t.Fatalf("record=%+v err=%v", record, err)
			}
			if redo {
				if _, err := m.Undo(ctx, record.ID); err != nil {
					t.Fatal(err)
				}
			}
			nestedFixtureGit(t, nested, "init")
			state := nestedFixtureTree(t, nested)
			operation := m.Undo
			if redo {
				operation = m.Redo
			}
			result, err := operation(ctx, record.ID)
			if err == nil || !strings.Contains(err.Error(), "nested repository") || len(result.Conflicts) != 1 {
				t.Fatalf("restore crossed new repository boundary: result=%+v err=%v", result, err)
			}
			want := "after"
			if redo {
				want = "before"
			}
			assertFileBytes(t, app, []byte(want))
			if got := nestedFixtureTree(t, nested); !reflect.DeepEqual(got, state) {
				t.Fatal("refused restore changed the nested repository")
			}
			if m.Latest("later-repo").Undone != redo {
				t.Fatal("refused restore changed checkpoint metadata")
			}
			assertNoRestoreTemporaries(t, home)
		})
	}
}

func TestSnapshotStillRefusesSymlinks(t *testing.T) {
	t.Parallel()
	for _, roots := range [][]string{nil, {"."}} {
		name := "workspace"
		if roots != nil {
			name = "explicit-root"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			target := filepath.Join(t.TempDir(), "target.txt")
			writeCheckpointFixture(t, target, "outside")
			if err := os.Symlink(target, filepath.Join(home, "link.txt")); err != nil {
				t.Skipf("file symlinks unavailable: %v", err)
			}
			m := openSnapshotTestManager(t, home)
			if _, err := m.captureSnapshot(context.Background(), roots, "", nil); err == nil || !strings.Contains(err.Error(), "non-regular") {
				t.Fatalf("symlink was silently omitted: %v", err)
			}
			assertFileBytes(t, target, []byte("outside"))
		})
	}
}
