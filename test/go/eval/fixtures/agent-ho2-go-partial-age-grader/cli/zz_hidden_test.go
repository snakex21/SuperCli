package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"snapkeep/snap"
)

func hzDir(t *testing.T, ages ...time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range ages {
		if err := os.WriteFile(filepath.Join(dir, snap.Name(hzNow.Add(-a))), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func hzAges(t *testing.T, dir string) []time.Duration {
	t.Helper()
	left, err := snap.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []time.Duration
	for _, s := range left {
		out = append(out, hzNow.Sub(s.Taken))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func hzPrune(t *testing.T, dir string, extra ...string) {
	t.Helper()
	args := append([]string{"prune", "--dir", dir}, extra...)
	if err := Run(args, &strings.Builder{}, hzNow); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

var hzNow = time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC)

const hzDay = 24 * time.Hour

func TestHiddenOlderThanFormats(t *testing.T) {
	cases := []struct {
		age  string
		want int // snapshots left of 1h, 30h, 3d+1h, 9d, 16d, 40d
	}{
		{"2w", 4}, {"10d", 4}, {"10", 4}, {"36h", 2}, {"1w", 3}, {"20d", 5}, {"3d", 2}, {" 2W ", 4},
	}
	for _, c := range cases {
		dir := hzDir(t, time.Hour, 30*time.Hour, 3*hzDay+time.Hour, 9*hzDay, 16*hzDay, 40*hzDay)
		hzPrune(t, dir, "--keep", "0", "--older-than", c.age)
		if got := len(hzAges(t, dir)); got != c.want {
			t.Errorf("--older-than %q: %d left, want %d", c.age, got, c.want)
		}
	}
}

func TestHiddenOlderThanKeepsNewest(t *testing.T) {
	dir := hzDir(t, 20*hzDay, 30*hzDay, 40*hzDay, 50*hzDay)
	hzPrune(t, dir, "--keep", "2", "--older-than", "1w")
	got := hzAges(t, dir)
	if len(got) != 2 || got[0] != 20*hzDay || got[1] != 30*hzDay {
		t.Fatalf("left %v", got)
	}
	dir = hzDir(t, hzDay, 2*hzDay, 3*hzDay, 4*hzDay, 5*hzDay, 6*hzDay, 30*hzDay, 31*hzDay)
	hzPrune(t, dir, "--older-than", "2w") // default --keep 5
	if got := len(hzAges(t, dir)); got != 6 {
		t.Fatalf("default keep: %d left, want 6", got)
	}
}

func TestHiddenOlderThanDryRunAndErrors(t *testing.T) {
	dir := hzDir(t, hzDay, 30*hzDay)
	hzPrune(t, dir, "--keep", "0", "--older-than", "10d", "--dry-run")
	if got := len(hzAges(t, dir)); got != 2 {
		t.Fatalf("dry run deleted files: %d left", got)
	}
	for _, bad := range []string{"soon", "0d", "-3d", "1y"} {
		if err := Run([]string{"prune", "--dir", dir, "--older-than", bad}, &strings.Builder{}, hzNow); err == nil {
			t.Errorf("--older-than %q: want error", bad)
		}
	}
	if got := len(hzAges(t, dir)); got != 2 {
		t.Fatalf("bad ages deleted files: %d left", got)
	}
}

func TestHiddenUsesNameNotMtime(t *testing.T) {
	dir := hzDir(t, 30*hzDay, hzDay)
	old := time.Now().Add(-1000 * hzDay)
	for _, s := range hzList(t, dir) {
		os.Chtimes(filepath.Join(dir, s.Name), old, old)
	}
	hzPrune(t, dir, "--keep", "0", "--older-than", "10d")
	got := hzAges(t, dir)
	if len(got) != 1 || got[0] != hzDay {
		t.Fatalf("left %v", got)
	}
}

func TestHiddenListFormatsUnchanged(t *testing.T) {
	dir := hzDir(t, time.Hour, 30*time.Hour, 3*hzDay+time.Hour, 9*hzDay)
	for arg, want := range map[string]int{"36h": 2, "10": 4, "1w": 3, "2d": 2} {
		var b strings.Builder
		if err := Run([]string{"list", "--dir", dir, "--max-age", arg}, &b, hzNow); err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(b.String(), "\n"); got != want {
			t.Errorf("list --max-age %s: %d lines, want %d", arg, got, want)
		}
	}
}

func TestHiddenPruneWithoutOlderThan(t *testing.T) {
	dir := hzDir(t, hzDay, 2*hzDay, 3*hzDay)
	hzPrune(t, dir, "--keep", "1")
	if got := hzAges(t, dir); len(got) != 1 || got[0] != hzDay {
		t.Fatalf("left %v", got)
	}
}

func hzList(t *testing.T, dir string) []snap.Snapshot {
	t.Helper()
	s, err := snap.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
