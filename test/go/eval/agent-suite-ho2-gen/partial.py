"""Partial information: the request leaves the value format (and the meaning of "age") to the
existing code; both must be read from it rather than guessed."""
from common import Task, d, cmd, go_filler, GO_MOD, sub


def build():
    t = Task("go-partial-age")
    t.fx("go.mod", GO_MOD.format(mod="snapkeep"))
    t.fx("snap/snap.go", d('''
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
        '''))
    t.fx("snap/snap_test.go", d('''
        package snap

        import (
            "testing"
            "time"
        )

        func TestParseName(t *testing.T) {
            at := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
            s, ok := Parse(Name(at))
            if !ok || !s.Taken.Equal(at) {
                t.Fatalf("%v %v", s, ok)
            }
            if _, ok := Parse("notes.txt"); ok {
                t.Fatal("notes.txt is not a snapshot")
            }
        }
        '''))
    age_go = d('''
        package cli

        import (
            "fmt"
            "strconv"
            "strings"
            "time"
        )

        // parseAge reads a positive age: a whole number followed by a unit,
        // h (hours), d (days) or w (weeks). A bare number means days.
        func parseAge(s string) (time.Duration, error) {
            s = strings.TrimSpace(strings.ToLower(s))
            unit := 24 * time.Hour
            switch {
            case strings.HasSuffix(s, "h"):
                unit, s = time.Hour, strings.TrimSuffix(s, "h")
            case strings.HasSuffix(s, "d"):
                s = strings.TrimSuffix(s, "d")
            case strings.HasSuffix(s, "w"):
                unit, s = 7*24*time.Hour, strings.TrimSuffix(s, "w")
            }
            n, err := strconv.Atoi(s)
            if err != nil || n <= 0 {
                return 0, fmt.Errorf("bad age %q: want e.g. 36h, 10d, 2w or a number of days", s)
            }
            return time.Duration(n) * unit, nil
        }
        ''')
    t.fx("cli/age.go", age_go)
    cli_go = d('''
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
                fmt.Fprintf(out, "%s\\t%s\\n", s.Name, age.Round(time.Hour))
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
                    fmt.Fprintf(out, "would delete %s\\n", s.Name)
                    continue
                }
                if err := os.Remove(filepath.Join(*dir, s.Name)); err != nil {
                    return err
                }
                fmt.Fprintf(out, "deleted %s\\n", s.Name)
            }
            return nil
        }
        ''')
    t.fx("cli/cli.go", cli_go)
    cli_test = d('''
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
            if n := strings.Count(b.String(), "\\n"); n != 2 {
                t.Fatalf("list printed %d lines:\\n%s", n, b.String())
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
        ''')
    t.fx("cli/cli_test.go", cli_test)
    t.fx("cmd/snapkeep/main.go", d('''
        // Command snapkeep lists and prunes snapshot archives.
        package main

        import (
            "fmt"
            "os"
            "time"

            "snapkeep/cli"
        )

        func main() {
            if err := cli.Run(os.Args[1:], os.Stdout, time.Now()); err != nil {
                fmt.Fprintln(os.Stderr, "snapkeep:", err)
                os.Exit(1)
            }
        }
        '''))
    readme = d('''
        # snapkeep

        Keeps a directory of `snap-<UTC timestamp>.tar.zst` archives tidy.

        ## Usage

            snapkeep list  [--dir DIR] [--max-age AGE]
            snapkeep prune [--dir DIR] [--keep N] [--dry-run]

        `list` prints every snapshot with its age; `--max-age` hides older ones.
        `prune` deletes everything except the newest N snapshots (default 5);
        `--dry-run` only prints what it would delete.
        ''')
    t.fx("README.md", readme)
    for p in ["remote", "notify"]:
        go_filler(t, p, count=3)

    t.ref("cli/cli.go", sub(cli_go,
        ('    dryRun := fs.Bool("dry-run", false, "only print what would be deleted")\n',
         '    dryRun := fs.Bool("dry-run", false, "only print what would be deleted")\n'
         '    olderThan := fs.String("older-than", "", "only delete snapshots older than this age")\n'),
        ('''    if *keep < 0 {
                return errors.New("--keep must not be negative")
            }
        '''.replace("\n        ", "\n"),
         '''    if *keep < 0 {
                return errors.New("--keep must not be negative")
            }
            var minAge time.Duration
            if *olderThan != "" {
                d, err := parseAge(*olderThan)
                if err != nil {
                    return fmt.Errorf("--older-than: %w", err)
                }
                minAge = d
            }
        '''.replace("\n        ", "\n")),
        ('''    for _, s := range snaps[:len(snaps)-*keep] {
                if *dryRun {'''.replace("\n        ", "\n"),
         '''    for _, s := range snaps[:len(snaps)-*keep] {
                if minAge > 0 && now.Sub(s.Taken) <= minAge {
                    continue
                }
                if *dryRun {'''.replace("\n        ", "\n"))))
    t.ref("cli/older_test.go", d('''
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
        '''))
    t.ref("README.md", sub(readme,
        ("    snapkeep prune [--dir DIR] [--keep N] [--dry-run]",
         "    snapkeep prune [--dir DIR] [--keep N] [--older-than AGE] [--dry-run]"),
        ("`--dry-run` only prints what it would delete.",
         "`--older-than` additionally limits deletion to snapshots older than AGE\n"
         "(the newest N are still kept). `--dry-run` only prints what it would delete.\n\n"
         "AGE is a whole number with a unit: `36h`, `10d`, `2w`; a bare number means days.")))
    t.answer("Added `prune --older-than AGE`. It reuses parseAge, so it takes the same format as `list --max-age` "
             "(36h, 10d, 2w, or a bare number of days), and age is measured from the timestamp in the snapshot name, "
             "like list does. --keep still protects the newest N snapshots and --dry-run still only prints. Added "
             "cli/older_test.go and documented the flag in the README.\n")
    t.gr("cli/zz_hidden_test.go", d('''
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
                if got := strings.Count(b.String(), "\\n"); got != want {
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
        '''))
    t.gr("graders/check_test_added.py", d('''
        """Partial credit: one of the agent's cli/*_test.go files exercises --older-than."""
        import glob
        import sys

        for path in sorted(glob.glob("cli/*_test.go")):
            if path.replace("\\\\", "/").endswith("zz_hidden_test.go"):
                continue
            with open(path, encoding="utf-8") as f:
                if "older-than" in f.read():
                    print("test found in", path)
                    sys.exit(0)
        print("no cli test mentions --older-than")
        sys.exit(1)
        '''))
    t.register(
        name="Add a prune option whose value format and meaning must be taken from existing code, not guessed",
        category="partial-info", tags=["go", "partial-info", "cli", "read-the-code"], difficulty="medium",
        prompt=("Add an `--older-than` option to `snapkeep prune` so it only deletes snapshots older than the given "
                "age. It must accept exactly the same age values as `list --max-age` does, and \"age\" means the same "
                "thing it means for `list`. `--keep` still applies on top of it (the newest N snapshots are never "
                "deleted) and `--dry-run` must keep working. Add a test for the new option and update the usage "
                "section of the README. `go vet ./...` and `go test ./...` must pass."),
        strictDiff=True,
        allowedChangedFiles=["cli/*.go", "README.md"],
        forbiddenChangedFiles=["snap/**", "cmd/**", "remote/**", "notify/**", "go.mod"],
        expectedChangedFiles=["cli/cli.go", "README.md"],
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./..."),
                              cmd("test-added", "python", "graders/check_test_added.py", optional=True, weight=0.5)],
        fileChecks=[
            {"id": "readme-option", "path": "README.md", "regex": [r"--older-than"]},
        ],
    )
