// Command logtail prints the log lines that match the filters.
//
//	logtail [--level LEVEL] [--now RFC3339] [FILE...]
//
// With no FILE it reads standard input. Lines that cannot be parsed are
// skipped with a warning on stderr. --now fixes the reference time
// (default: the current time); it exists for reproducible runs.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"logtail/internal/logline"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logtail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	level := fs.String("level", "debug", "minimum level to show")
	nowFlag := fs.String("now", "", "reference time (RFC 3339), default: now")
	since := fs.String("since", "", "show entries at or after this time: a duration before --now (15m) or an RFC 3339 time")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	minLevel, err := logline.ParseLevel(*level)
	if err != nil {
		fmt.Fprintln(stderr, "logtail: --level:", err)
		return 2
	}
	now := time.Now()
	if *nowFlag != "" {
		if now, err = time.Parse(time.RFC3339, *nowFlag); err != nil {
			fmt.Fprintln(stderr, "logtail: --now:", err)
			return 2
		}
	}
	var cutoff time.Time
	if *since != "" {
		if cutoff, err = parseSince(*since, now); err != nil {
			fmt.Fprintln(stderr, "logtail: --since:", err)
			return 2
		}
	}

	inputs := []io.Reader{stdin}
	if fs.NArg() > 0 {
		inputs = nil
		for _, name := range fs.Args() {
			f, err := os.Open(name)
			if err != nil {
				fmt.Fprintln(stderr, "logtail:", err)
				return 1
			}
			defer f.Close()
			inputs = append(inputs, f)
		}
	}
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	for _, in := range inputs {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			e, err := logline.Parse(sc.Text())
			if err != nil {
				fmt.Fprintln(stderr, "logtail: skipping:", err)
				continue
			}
			if e.Level < minLevel {
				continue
			}
			if !cutoff.IsZero() && e.Time.Before(cutoff) {
				continue
			}
			fmt.Fprintln(out, e.Raw)
		}
	}
	return 0
}

// parseSince accepts a duration (relative to now) or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return time.Time{}, fmt.Errorf("%q is neither a duration (e.g. 15m) nor an RFC 3339 time", s)
	}
	return now.Add(-d), nil
}
