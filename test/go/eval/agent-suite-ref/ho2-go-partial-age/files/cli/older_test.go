package cli

import (
	"strings"
	"testing"
	"time"

	"snapkeep/snap"
)

func TestPruneOlderThan(t *testing.T) {
	day := 24 * time.Hour
	dir := setup(t, day, 5*day, 12*day, 20*day)
	if err := Run([]string{"prune", "--dir", dir, "--keep", "0", "--older-than", "10d"}, &strings.Builder{}, now); err != nil {
		t.Fatal(err)
	}
	left, _ := snap.List(dir)
	if len(left) != 2 {
		t.Fatalf("left %v", left)
	}
}
