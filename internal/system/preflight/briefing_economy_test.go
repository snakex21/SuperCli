package preflight

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/system/childproc"
)

func TestRecentCommitLinesOnlyDropsExactFirstHEAD(t *testing.T) {
	for _, tc := range []struct {
		name, head, log string
		want            []string
	}{
		{"empty", "", "", nil},
		{"single HEAD", "abc123 first", "abc123 first", []string{}},
		{"exact duplicate", "abc123 first", "abc123 first\nxyz789 older", []string{"xyz789 older"}},
		{"same hash different text", "abc123 first", "abc123 different\nxyz789 older", []string{"abc123 different", "xyz789 older"}},
		{"HEAD missing", "", "abc123 first", []string{"abc123 first"}},
		{"later identical entry retained", "abc123 first", "xyz789 older\nabc123 first", []string{"xyz789 older", "abc123 first"}},
		{"CRLF source", "abc123 pierwszy \U0001f4dd", "abc123 pierwszy \U0001f4dd\r\nxyz789 starszy\r\n", []string{"xyz789 starszy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := recentCommitLines(tc.head, tc.log); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestBriefingHEADAppearsOnceWithUniqueCommitFacts(t *testing.T) {
	head := "abc123 finish command cancellation safely"
	older := "ddd567 preserve every independent verification result"
	status := " M internal/service/handler.go\n?? tests/handler_test.go"
	block := Build(t.TempDir(), Options{
		Budget: 1000, LookPath: hasGit,
		RunGit: gitStub("main", head, status, head+"\n"+older),
	})
	for _, fact := range []string{"branch: main", "HEAD: " + head, status, "recent commits:\n" + older} {
		if !strings.Contains(block, fact) {
			t.Fatalf("unique fact %q missing from %q", fact, block)
		}
	}
	if strings.Count(block, head) != 1 {
		t.Fatalf("HEAD is repeated: %q", block)
	}
	previous := strings.Replace(block, "recent commits:\n", "recent commits:\n"+head+"\n", 1)
	if before, after := EstimateTokens(previous), EstimateTokens(block); after >= before {
		t.Fatalf("duplicate removal did not shorten briefing: %d -> %d", before, after)
	} else {
		t.Logf("identical unique facts, estimated briefing tokens: %d -> %d", before, after)
	}
}

func TestBriefingOneCommitKeepsHEADWithoutEmptySection(t *testing.T) {
	head := "abc123 only commit"
	block := Build(t.TempDir(), Options{LookPath: hasGit, RunGit: gitStub("main", head, "", head)})
	if !strings.Contains(block, "HEAD: "+head) || !strings.Contains(block, "working tree clean") ||
		strings.Contains(block, "recent commits:") || strings.Count(block, head) != 1 {
		t.Fatalf("single-commit identity or section changed: %q", block)
	}
}

// Use real Git stdout: canned RunGit fixtures already preserved leading
// columns, so they could not detect realRunGit's former TrimSpace corruption.
func TestRealGitStatusPreservesPorcelainColumns(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is optional; no executable available")
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, git, append([]string{"-C", root}, args...)...)
		childproc.HideWindow(cmd)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "absent-global-config"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	writePF(t, filepath.Join(root, "first.go"), "before\n")
	run("add", "first.go")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false",
		"-c", "core.hooksPath="+filepath.Join(root, "absent-hooks"), "commit", "-m", "fixture")
	writePF(t, filepath.Join(root, "first.go"), "changed body\n")
	status, err := realRunGit(ctx, root, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status != " M first.go" {
		t.Fatalf("porcelain columns changed: %q", status)
	}
	if code, path := porcelainParts(status); code != " M" || path != "first.go" {
		t.Fatalf("parsed status/path = %q/%q", code, path)
	}
	for i := 0; i < defaultMaxStatusFiles; i++ {
		status += fmt.Sprintf("\n?? extra-%02d.go", i)
	}
	compact := strings.Join(compactStatus(status), "\n")
	if !strings.Contains(compact, "modified 1") || !strings.Contains(compact, "sample: first.go,") {
		t.Fatalf("first status/path corrupted in large summary: %q", compact)
	}
}

var preflightBriefingSink string

// Identical canned Git state, normal production collection and budget assembly.
// No model/network latency is included, and no status/static cache is injected.
func BenchmarkPreflightBriefingBuild(b *testing.B) {
	var log strings.Builder
	for i := 0; i < defaultMaxCommits; i++ {
		fmt.Fprintf(&log, "abc%04d preserve scoped command results and checked file edits\n", i)
	}
	for _, count := range []int{0, 6, 500} {
		b.Run(fmt.Sprintf("status_%d", count), func(b *testing.B) {
			root := b.TempDir()
			var status strings.Builder
			for i := 0; i < count; i++ {
				fmt.Fprintf(&status, " M internal/service/file-%04d.go\n", i)
			}
			stub := gitStub("main", "", status.String(), log.String())
			var calls atomic.Int64
			options := Options{LookPath: hasGit, RunGit: func(root string, args ...string) (string, error) {
				calls.Add(1)
				return stub(root, args...)
			}}
			block := Build(root, options)
			if block == "" || EstimateTokens(block) > DefaultBudget {
				b.Fatal("invalid briefing")
			}
			calls.Store(0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				preflightBriefingSink = Build(root, options)
			}
			b.StopTimer()
			b.ReportMetric(float64(calls.Load())/float64(b.N), "git-calls/op")
			b.ReportMetric(float64(EstimateTokens(block)), "estimated-tokens")
		})
	}
}
