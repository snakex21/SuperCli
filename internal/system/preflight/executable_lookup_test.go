package preflight

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/system/childproc"
)

// Frozen independent pre-edit runner: every command resolves "git" again.
// Keep this oracle independent of the production executable helper.
func originalExecutableRunGit(ctx context.Context, root string, args ...string) (string, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	childproc.HideWindow(cmd)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if len(args) > 0 && args[0] == "status" {
		return strings.TrimRight(string(out), "\r\n"), err
	}
	return strings.TrimSpace(string(out)), err
}

func originalExecutableBuild(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	return BuildContext(ctx, root, Options{
		LookPath: exec.LookPath,
		RunGit: func(root string, args ...string) (string, error) {
			return originalExecutableRunGit(ctx, root, args...)
		},
	})
}

func TestResolvedGitExecutableHasIdenticalNativeBriefing(t *testing.T) {
	for _, count := range []int{0, 6, 200} {
		root := preflightNativeFixture(t, count)
		// Explicit Now disables only the existing short log cache, so both
		// sides perform the same two native commands on the same fresh state.
		want := originalExecutableBuild(root)
		if got := Build(root, Options{Now: time.Unix(1, 0)}); got != want {
			t.Fatalf("resolved executable changed %d-path briefing:\ngot %q\nwant %q", count, got, want)
		}
		if err := os.WriteFile(filepath.Join(root, "tracked.go"), []byte("fresh edit after collection\n"), 0600); err != nil {
			t.Fatal(err)
		}
		want = originalExecutableBuild(root)
		if got := Build(root, Options{Now: time.Unix(1, 0)}); got != want ||
			!(strings.Contains(got, " M tracked.go") || strings.Contains(got, "(modified 1,")) {
			t.Fatalf("fresh state not preserved:\ngot %q\nwant %q", got, want)
		}
	}
}

func TestResolvedGitExecutablePreservesCustomRunnerContract(t *testing.T) {
	root := t.TempDir()
	var lookups int
	var mu sync.Mutex
	var commands [][]string
	block := Build(root, Options{
		LookPath: func(name string) (string, error) {
			lookups++
			if name != "git" {
				t.Fatalf("changed lookup name: %q", name)
			}
			// Must never execute this path while a caller supplies RunGit.
			return filepath.Join(root, "not-an-executable"), nil
		},
		RunGit: func(gotRoot string, args ...string) (string, error) {
			if gotRoot != root {
				t.Errorf("changed runner root: %q", gotRoot)
			}
			mu.Lock()
			commands = append(commands, append([]string(nil), args...))
			mu.Unlock()
			if args[0] == "status" {
				return "## feature/żółć\n M \"odd name.go\"", nil
			}
			return "abc123 first\ndef456 second", nil
		},
	})
	if lookups != 1 || len(commands) != 2 || !strings.Contains(block, " M \"odd name.go\"") {
		t.Fatalf("changed custom collector: lookups=%d commands=%v block=%q", lookups, commands, block)
	}
	for _, args := range commands {
		want := "status --porcelain --branch"
		if args[0] == "log" {
			want = "log --oneline -8"
		}
		if strings.Join(args, " ") != want {
			t.Fatalf("changed runner argv: %v", args)
		}
	}
}

func TestResolvedGitExecutableDoesNotIgnoreLookupErrors(t *testing.T) {
	root := t.TempDir()
	writePF(t, filepath.Join(root, "fresh-local.go"), "local data")
	now := time.Now()
	want := Build(root, Options{LookPath: noGit, Now: now})
	for _, cause := range []error{exec.ErrDot, exec.ErrNotFound, os.ErrPermission} {
		got := Build(root, Options{Now: now,
			LookPath: func(string) (string, error) { return "unusable-git.exe", &exec.Error{Name: "git", Err: cause} },
			RunGit: func(string, ...string) (string, error) {
				t.Fatal("started runner after rejected lookup")
				return "", nil
			},
		})
		if got != want {
			t.Fatalf("lookup error %v changed fallback: got %q want %q", cause, got, want)
		}
	}
}

func TestResolvedGitExecutableIsNotCachedAcrossCollections(t *testing.T) {
	root := preflightNativeFixture(t, 0)
	if got := Build(root, Options{}); !strings.Contains(got, "branch: fixture-branch") {
		t.Fatalf("native fixture not collected: %q", got)
	}
	// This empty portable PATH prevents a new native lookup. A persistent
	// executable cache would incorrectly keep running the previous Git.
	t.Setenv("PATH", t.TempDir())
	if got := Build(root, Options{}); strings.Contains(got, "branch:") || !strings.Contains(got, "tracked.go") {
		t.Fatalf("previous Git executable leaked into later collection: %q", got)
	}
}

func TestResolvedGitExecutableKeepsForegroundCancellation(t *testing.T) {
	executable, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git optional")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := realRunGitExecutable(ctx, executable, t.TempDir(), "status", "--porcelain", "--branch"); !errors.Is(err, context.Canceled) || out != "" {
		t.Fatalf("resolved native command ignored foreground cancellation: %q %v", out, err)
	}
}

var preflightCommandLookupSink *exec.Cmd

func benchmarkGitCommandLookup(b *testing.B) {
	for _, implementation := range []string{"original", "resolved"} {
		b.Run(implementation, func(b *testing.B) {
			b.ReportAllocs()
			ctx := context.Background()
			for b.Loop() {
				executable, err := exec.LookPath("git")
				if err != nil {
					b.Fatal(err)
				}
				if implementation == "original" {
					executable = "git"
				}
				for _, args := range [][]string{{"-C", ".", "status", "--porcelain", "--branch"}, {"-C", ".", "log", "--oneline", "-8"}} {
					preflightCommandLookupSink = exec.CommandContext(ctx, executable, args...)
					if preflightCommandLookupSink.Err != nil {
						b.Fatal(preflightCommandLookupSink.Err)
					}
				}
			}
			lookups := 1
			if implementation == "original" {
				lookups = 3
			}
			b.ReportMetric(float64(lookups), "PATH-resolutions/op")
		})
	}
}

func BenchmarkPreflightGitCommandLookupCurrentPATH(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("Git optional")
	}
	benchmarkGitCommandLookup(b)
}

func BenchmarkPreflightGitCommandLookupLongPATH(b *testing.B) {
	root := b.TempDir()
	paths := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		dir := filepath.Join(root, "empty-"+itoa(i))
		if err := os.Mkdir(dir, 0700); err != nil {
			b.Fatal(err)
		}
		paths = append(paths, dir)
	}
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
		b.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	if err := os.WriteFile(filepath.Join(paths[len(paths)-1], name), []byte("lookup only; never executed"), 0700); err != nil {
		b.Fatal(err)
	}
	b.Setenv("PATH", strings.Join(paths, string(os.PathListSeparator)))
	benchmarkGitCommandLookup(b)
}

func BenchmarkPreflightResolvedGitNativeCold(b *testing.B) {
	for _, implementation := range []string{"original", "production"} {
		b.Run(implementation, func(b *testing.B) {
			root := preflightNativeFixture(b, 6)
			want := originalExecutableBuild(root)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var got string
				if implementation == "original" {
					got = originalExecutableBuild(root)
				} else {
					got = Build(root, Options{Now: time.Unix(1, 0)})
				}
				if got != want {
					b.Fatal("briefing changed")
				}
				preflightBriefingSink = got
			}
		})
	}
}
