// Package cli implements the snapkeep command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"snapkeep/snap"
)

// Run executes one snapkeep command. now is the reference time for ages.
func Run(args []string, out io.Writer, now time.Time) error {
	if len(args) == 0 {
		return errors.New("usage: snapkeep list|prune [flags]")
	}
	switch args[0] {
	case "list":
		return list(args[1:], out, now)
	case "prune":
		return prune(args[1:], out, now)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func list(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", ".", "snapshot directory")
	maxAge := fs.String("max-age", "", "only show snapshots at most this old")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var limit time.Duration
	if *maxAge != "" {
		d, err := parseAge(*maxAge)
		if err != nil {
			return fmt.Errorf("--max-age: %w", err)
		}
		limit = d
	}
	snaps, err := snap.List(*dir)
	if err != nil {
		return err
	}
	for _, s := range snaps {
		age := now.Sub(s.Taken)
		if limit > 0 && age > limit {
			continue
		}
		fmt.Fprintf(out, "%s\t%s\n", s.Name, age.Round(time.Hour))
	}
	return nil
}

func prune(args []string, out io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", ".", "snapshot directory")
	keep := fs.Int("keep", 5, "always keep the newest N snapshots")
	dryRun := fs.Bool("dry-run", false, "only print what would be deleted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keep < 0 {
		return errors.New("--keep must not be negative")
	}
	snaps, err := snap.List(*dir)
	if err != nil {
		return err
	}
	if len(snaps) <= *keep {
		return nil
	}
	for _, s := range snaps[:len(snaps)-*keep] {
		if *dryRun {
			fmt.Fprintf(out, "would delete %s\n", s.Name)
			continue
		}
		if err := os.Remove(filepath.Join(*dir, s.Name)); err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted %s\n", s.Name)
	}
	return nil
}
