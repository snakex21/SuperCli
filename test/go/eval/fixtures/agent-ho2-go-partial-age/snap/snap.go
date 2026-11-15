// Package snap finds snapshot archives in a directory.
package snap

import (
	"os"
	"sort"
	"strings"
	"time"
)

// Prefix and Suffix frame the UTC timestamp in a snapshot file name:
// snap-20270301T120000Z.tar.zst
const (
	Prefix = "snap-"
	Suffix = ".tar.zst"
	layout = "20060102T150405Z"
)

// Snapshot is one archive. Taken comes from the file name, never from the
// file's modification time (copies and restores reset mtimes).
type Snapshot struct {
	Name  string
	Taken time.Time
}

// Parse recognises a snapshot file name.
func Parse(name string) (Snapshot, bool) {
	if !strings.HasPrefix(name, Prefix) || !strings.HasSuffix(name, Suffix) {
		return Snapshot{}, false
	}
	ts := strings.TrimSuffix(strings.TrimPrefix(name, Prefix), Suffix)
	at, err := time.Parse(layout, ts)
	if err != nil {
		return Snapshot{}, false
	}
	return Snapshot{Name: name, Taken: at}, true
}

// List returns the snapshots in dir, oldest first. Other files are ignored.
func List(dir string) ([]Snapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if s, ok := Parse(e.Name()); ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Taken.Before(out[j].Taken) })
	return out, nil
}

// Name builds the file name for a snapshot taken at t.
func Name(t time.Time) string {
	return Prefix + t.UTC().Format(layout) + Suffix
}
