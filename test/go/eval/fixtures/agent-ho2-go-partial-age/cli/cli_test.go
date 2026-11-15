package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"snapkeep/snap"
)

var now = time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T, ages ...time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range ages {
		if err := os.WriteFile(filepath.Join(dir, snap.Name(now.Add(-a))), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "README.txt"), nil, 0o644)
	return dir
}

func TestList(t *testing.T) {
	dir := setup(t, time.Hour, 72*time.Hour, 30*24*time.Hour)
	var b strings.Builder
	if err := Run([]string{"list", "--dir", dir, "--max-age", "1w"}, &b, now); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "\n"); n != 2 {
		t.Fatalf("list printed %d lines:\n%s", n, b.String())
	}
}

func TestPruneKeep(t *testing.T) {
	dir := setup(t, time.Hour, 2*time.Hour, 3*time.Hour, 4*time.Hour)
	var b strings.Builder
	if err := Run([]string{"prune", "--dir", dir, "--keep", "1"}, &b, now); err != nil {
		t.Fatal(err)
	}
	left, _ := snap.List(dir)
	if len(left) != 1 || !left[0].Taken.Equal(now.Add(-time.Hour)) {
		t.Fatalf("left %v", left)
	}
}

func TestBadAge(t *testing.T) {
	if err := Run([]string{"list", "--max-age", "soon"}, &strings.Builder{}, now); err == nil {
		t.Fatal("want error")
	}
}
