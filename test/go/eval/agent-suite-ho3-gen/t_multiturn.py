"""Multi-turn tasks: a follow-up that reverts one change but keeps the rest,
and a follow-up that corrects the specification of the first turn."""
from common import GRADER_PRELUDE, Task, cmd, d, sub, turns

UNITTEST = ["python", "-m", "unittest", "discover", "-s", "tests", "-t", "."]

STOCK_PY = d('''
    """Stock levels for the warehouse service."""
    from dataclasses import dataclass


    class OutOfStock(Exception):
        """Raised when a reservation exceeds the available quantity."""


    @dataclass
    class StockItem:
        sku: str
        qty_on_hand: int
        reserved: int = 0

        @property
        def available(self):
            return self.qty_on_hand - self.reserved


    class Stock:
        """In-memory stock book keyed by SKU."""

        def __init__(self):
            self._items = {}

        def add(self, sku, qty):
            """Receive qty units of sku into the warehouse."""
            item = self._items.get(sku)
            if item is None:
                item = self._items[sku] = StockItem(sku, 0)
            item.qty_on_hand += qty
            return item

        def get(self, sku):
            return self._items[sku]

        def reserve(self, sku, qty):
            """Reserve qty units for an order; returns what is still available."""
            item = self._items[sku]
            if qty > item.available:
                raise OutOfStock("%s: requested %d, available %d" % (sku, qty, item.available))
            item.reserved += qty
            return item.available

        def ship(self, sku, qty):
            """Ship qty reserved units: they leave the warehouse."""
            item = self._items[sku]
            if qty > item.reserved:
                raise OutOfStock("%s: shipping %d, reserved %d" % (sku, qty, item.reserved))
            item.reserved -= qty
            item.qty_on_hand -= qty

        def skus(self):
            return sorted(self._items)
    ''')

STOCK_SOLVED = sub(
    STOCK_PY,
    ('        """Receive qty units of sku into the warehouse."""\n',
     '        """Receive qty units of sku into the warehouse."""\n'
     '        if qty <= 0:\n'
     '            raise ValueError("quantity must be positive, got %d" % qty)\n'),
    ('        """Reserve qty units for an order; returns what is still available."""\n',
     '        """Reserve qty units for an order; returns what is still available."""\n'
     '        if qty <= 0:\n'
     '            raise ValueError("quantity must be positive, got %d" % qty)\n'),
    ('    def skus(self):',
     '    def release(self, sku, qty):\n'
     '        """Return qty reserved units to the available pool."""\n'
     '        if qty <= 0:\n'
     '            raise ValueError("quantity must be positive, got %d" % qty)\n'
     '        item = self._items[sku]\n'
     '        if qty > item.reserved:\n'
     '            raise ValueError("%s: releasing %d, reserved %d" % (sku, qty, item.reserved))\n'
     '        item.reserved -= qty\n'
     '        return item.available\n'
     '\n'
     '    def skus(self):'))

REPORTS_PY = d('''
    """Plain-text stock reports."""


    def low_stock(stock, threshold=5):
        """SKUs whose on-hand quantity is below threshold."""
        return [sku for sku in stock.skus() if stock.get(sku).qty_on_hand < threshold]


    def summary(stock):
        lines = []
        for sku in stock.skus():
            item = stock.get(sku)
            lines.append("%-10s on hand %4d  reserved %4d  available %4d"
                         % (sku, item.qty_on_hand, item.reserved, item.available))
        return "\\n".join(lines)
    ''')

API_PY = d('''
    """JSON views used by the HTTP layer (consumed by external clients)."""


    def to_dict(item):
        return {
            "sku": item.sku,
            "qty_on_hand": item.qty_on_hand,
            "reserved": item.reserved,
            "available": item.available,
        }


    def stock_to_list(stock):
        return [to_dict(stock.get(sku)) for sku in stock.skus()]
    ''')

TEST_STOCK = d('''
    import unittest

    from inventory.api import to_dict
    from inventory.reports import low_stock, summary
    from inventory.stock import OutOfStock, Stock


    class StockTest(unittest.TestCase):
        def setUp(self):
            self.stock = Stock()
            self.stock.add("A-100", 10)
            self.stock.add("B-200", 3)

        def test_add_accumulates(self):
            self.stock.add("A-100", 5)
            self.assertEqual(self.stock.get("A-100").qty_on_hand, 15)

        def test_reserve_and_ship(self):
            self.assertEqual(self.stock.reserve("A-100", 4), 6)
            self.stock.ship("A-100", 4)
            item = self.stock.get("A-100")
            self.assertEqual((item.qty_on_hand, item.reserved), (6, 0))

        def test_reserve_too_much(self):
            with self.assertRaises(OutOfStock):
                self.stock.reserve("B-200", 4)

        def test_reports(self):
            self.assertEqual(low_stock(self.stock), ["B-200"])
            self.assertIn("B-200", summary(self.stock))

        def test_api_view(self):
            self.assertEqual(to_dict(self.stock.get("B-200")),
                             {"sku": "B-200", "qty_on_hand": 3, "reserved": 0, "available": 3})


    if __name__ == "__main__":
        unittest.main()
    ''')


def build_undo():
    t = Task("multiturn-undo-rename")
    t.fx("README.md", "# inventory\n\nWarehouse stock book. Run the tests with `python -m unittest discover -s tests -t .`\n")
    t.fx("inventory/__init__.py", "")
    t.fx("inventory/stock.py", STOCK_PY)
    t.fx("inventory/reports.py", REPORTS_PY)
    t.fx("inventory/api.py", API_PY)
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_stock.py", TEST_STOCK)
    t.fx(".gitignore", "__pycache__/\n")

    t.gr("_ho3_grade/expected/reports.py", REPORTS_PY)
    t.gr("_ho3_grade/expected/api.py", API_PY)
    t.gr("_ho3_grade/expected/test_stock.py", TEST_STOCK)
    argv = t.grade_script(GRADER_PRELUDE + d(r'''
        import dataclasses
        import re
        import importlib

        sys.path.insert(0, os.getcwd())
        stock_mod = importlib.import_module("inventory.stock")
        api = importlib.import_module("inventory.api")
        Stock = stock_mod.Stock

        names = [f.name for f in dataclasses.fields(stock_mod.StockItem)]
        check("field qty_on_hand restored", "qty_on_hand" in names, names)
        check("no on_hand field left", "on_hand" not in names, names)

        s = Stock()
        s.add("X", 10)
        check("api key qty_on_hand", api.to_dict(s.get("X")).get("qty_on_hand") == 10, api.to_dict(s.get("X")))
        check("api has no on_hand key", "on_hand" not in api.to_dict(s.get("X")))

        def raises(exc, fn, *a):
            try:
                fn(*a)
            except exc:
                return True
            except Exception as e:  # wrong type
                return False
            return False

        for q in (0, -3):
            check("add(%d) raises ValueError" % q, raises(ValueError, Stock().add, "Y", q))
            st = Stock(); st.add("Y", 5)
            check("reserve(%d) raises ValueError" % q, raises(ValueError, st.reserve, "Y", q))
            check("reserve(%d) left stock untouched" % q, st.get("Y").reserved == 0 and st.get("Y").qty_on_hand == 5)

        st = Stock(); st.add("Z", 10); st.reserve("Z", 6)
        check("release exists", hasattr(st, "release"))
        if hasattr(st, "release"):
            st.release("Z", 4)
            check("release returns units", st.get("Z").reserved == 2 and st.get("Z").available == 8,
                  (st.get("Z").reserved, st.get("Z").available))
            check("release more than reserved raises ValueError", raises(ValueError, st.release, "Z", 3))
            check("failed release changed nothing", st.get("Z").reserved == 2)
            check("release(0) raises ValueError", raises(ValueError, st.release, "Z", 0))
        st = Stock(); st.add("Q", 4)
        check("valid add/reserve still work", st.reserve("Q", 4) == 0)

        # The rename must be gone everywhere (qty_on_hand does not match \bon_hand\b).
        leftovers = []
        for base in ("inventory", "tests"):
            for root, _, files in os.walk(base):
                for n in files:
                    if n.endswith(".py"):
                        p = os.path.join(root, n)
                        with open(p, encoding="utf-8") as f:
                            for i, line in enumerate(f, 1):
                                if re.search(r"\bon_hand\b", line):
                                    leftovers.append("%s:%d" % (p, i))
        check("no on_hand identifier left", not leftovers, leftovers[:5])

        # Files the rename touched and nothing else needed must be back to the original.
        for rel, exp in (("inventory/reports.py", "reports.py"), ("inventory/api.py", "api.py")):
            check(rel + " restored to original", norm_text(read_bytes(rel)) == norm_text(read_bytes(os.path.join("_ho3_grade", "expected", exp))))
        cur = norm_text(read_bytes("tests/test_stock.py"))
        orig = norm_text(read_bytes("_ho3_grade/expected/test_stock.py"))
        check("original tests kept (rename reverted in tests)", all(l in cur.split("\n") for l in orig.split("\n") if l.strip()))
        finish()
        '''))
    stock_solved = STOCK_SOLVED
    t.solve_files({"inventory/stock.py": stock_solved}, answer=(
        "Reverted the rename: `StockItem.qty_on_hand`, the `qty_on_hand` JSON key in `inventory/api.py`, "
        "`inventory/reports.py` and the tests are back to the original names. Kept the ValueError checks in "
        "`add()`/`reserve()` and the new `Stock.release()`. Tests pass."))
    t.register(
        name="Two-turn conversation: implement three changes, then undo only the rename",
        description=("Turn 1 asks for validation, a field rename across the package and a new method; turn 2 "
                     "asks to revert only the rename. Graded on the final state: rename fully reverted (field, "
                     "JSON key, other modules byte-equivalent to the original), the other two changes kept."),
        category="multi-turn",
        tags=["multi-turn", "revert", "python", "follow-up"],
        difficulty="medium",
        prompt=turns(
            """In the inventory package please make these three changes, then run the tests:
(a) `Stock.add()` and `Stock.reserve()` must raise `ValueError` when the quantity is zero or negative.
(b) Rename the `StockItem` field `qty_on_hand` to `on_hand` everywhere in the package and the tests (including the JSON key in `inventory/api.py`).
(c) Add `Stock.release(sku, qty)` that gives reserved units back to the available pool; releasing more than is reserved (or a non-positive quantity) raises `ValueError`.""",
            """I just talked to the API team: the rename breaks their clients. Please undo the rename from (b) completely - field name, JSON key, reports, tests, everything back to `qty_on_hand`. Keep (a) and (c) exactly as they are."""),
        timeoutSeconds=1500,
        expectedChangedFiles=["inventory/stock.py"],
        strictDiff=True,
        allowedChangedFiles=["inventory/**", "tests/**"],
        verificationCommands=[
            cmd("unit-tests", *UNITTEST),
            cmd("final-state", *argv, weight=3),
        ],
    )


# ---------------------------------------------------------------- logtail (Go)

LOGLINE_GO = d('''
    // Package logline parses the service log format:
    //
    //	2026-03-01T10:15:00Z WARN cache miss ratio above 0.4
    //
    // (RFC 3339 timestamp, level, message).
    package logline

    import (
    	"fmt"
    	"strings"
    	"time"
    )

    // Level orders log severities.
    type Level int

    const (
    	Debug Level = iota
    	Info
    	Warn
    	Error
    )

    var levelNames = map[string]Level{"DEBUG": Debug, "INFO": Info, "WARN": Warn, "ERROR": Error}

    // ParseLevel accepts a level name in any case.
    func ParseLevel(s string) (Level, error) {
    	l, ok := levelNames[strings.ToUpper(strings.TrimSpace(s))]
    	if !ok {
    		return 0, fmt.Errorf("unknown level %q", s)
    	}
    	return l, nil
    }

    // Entry is one parsed log line; Raw is the line as read.
    type Entry struct {
    	Time  time.Time
    	Level Level
    	Msg   string
    	Raw   string
    }

    // Parse parses one line.
    func Parse(line string) (Entry, error) {
    	fields := strings.SplitN(strings.TrimRight(line, "\\r\\n"), " ", 3)
    	if len(fields) < 2 {
    		return Entry{}, fmt.Errorf("malformed line %q", line)
    	}
    	ts, err := time.Parse(time.RFC3339, fields[0])
    	if err != nil {
    		return Entry{}, fmt.Errorf("bad timestamp: %w", err)
    	}
    	lvl, err := ParseLevel(fields[1])
    	if err != nil {
    		return Entry{}, err
    	}
    	e := Entry{Time: ts, Level: lvl, Raw: strings.TrimRight(line, "\\r\\n")}
    	if len(fields) == 3 {
    		e.Msg = fields[2]
    	}
    	return e, nil
    }
    ''')

LOGLINE_TEST = d('''
    package logline

    import "testing"

    func TestParse(t *testing.T) {
    	e, err := Parse("2026-03-01T10:15:00Z warn cache miss ratio above 0.4\\n")
    	if err != nil {
    		t.Fatal(err)
    	}
    	if e.Level != Warn || e.Msg != "cache miss ratio above 0.4" || e.Time.Minute() != 15 {
    		t.Fatalf("got %+v", e)
    	}
    }

    func TestParseErrors(t *testing.T) {
    	for _, line := range []string{"", "yesterday INFO x", "2026-03-01T10:15:00Z LOUD x"} {
    		if _, err := Parse(line); err == nil {
    			t.Errorf("Parse(%q) succeeded", line)
    		}
    	}
    }
    ''')

MAIN_GO = d('''
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
    	_ = now // reference time for time-based filters

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
    			fmt.Fprintln(out, e.Raw)
    		}
    	}
    	return 0
    }
    ''')

MAIN_TEST = d('''
    package main

    import (
    	"bytes"
    	"strings"
    	"testing"
    )

    const sample = `2026-03-01T10:00:00Z INFO started
    2026-03-01T10:05:00Z DEBUG warmup done
    2026-03-01T10:10:00Z WARN slow request /api/orders
    `

    func TestLevelFilter(t *testing.T) {
    	var out, errb bytes.Buffer
    	code := run([]string{"--level", "info"}, strings.NewReader(sample), &out, &errb)
    	if code != 0 {
    		t.Fatalf("exit %d: %s", code, errb.String())
    	}
    	want := "2026-03-01T10:00:00Z INFO started\\n2026-03-01T10:10:00Z WARN slow request /api/orders\\n"
    	if out.String() != want {
    		t.Fatalf("got %q", out.String())
    	}
    }

    func TestBadLevel(t *testing.T) {
    	var out, errb bytes.Buffer
    	if code := run([]string{"--level", "loud"}, strings.NewReader(sample), &out, &errb); code != 2 {
    		t.Fatalf("exit %d", code)
    	}
    }
    ''')

MAIN_SOLVED = sub(
    MAIN_GO,
    ('\tnowFlag := fs.String("now", "", "reference time (RFC 3339), default: now")\n',
     '\tnowFlag := fs.String("now", "", "reference time (RFC 3339), default: now")\n'
     '\tsince := fs.String("since", "", "show entries at or after this time: a duration before --now (15m) or an RFC 3339 time")\n'),
    ('\t_ = now // reference time for time-based filters\n',
     '\tvar cutoff time.Time\n'
     '\tif *since != "" {\n'
     '\t\tif cutoff, err = parseSince(*since, now); err != nil {\n'
     '\t\t\tfmt.Fprintln(stderr, "logtail: --since:", err)\n'
     '\t\t\treturn 2\n'
     '\t\t}\n'
     '\t}\n'),
    ('\t\t\tif e.Level < minLevel {\n\t\t\t\tcontinue\n\t\t\t}\n',
     '\t\t\tif e.Level < minLevel {\n\t\t\t\tcontinue\n\t\t\t}\n'
     '\t\t\tif !cutoff.IsZero() && e.Time.Before(cutoff) {\n\t\t\t\tcontinue\n\t\t\t}\n'),
) + (
    '\n// parseSince accepts a duration (relative to now) or an RFC 3339 time.\n'
    'func parseSince(s string, now time.Time) (time.Time, error) {\n'
    '\tif t, err := time.Parse(time.RFC3339, s); err == nil {\n\t\treturn t, nil\n\t}\n'
    '\td, err := time.ParseDuration(s)\n'
    '\tif err != nil || d < 0 {\n'
    '\t\treturn time.Time{}, fmt.Errorf("%q is neither a duration (e.g. 15m) nor an RFC 3339 time", s)\n'
    '\t}\n'
    '\treturn now.Add(-d), nil\n'
    '}\n')

CASES_LOG = d('''
    2026-03-01T09:59:59Z INFO before window
    2026-03-01T10:00:00Z DEBUG exactly ten
    2026-03-01T10:14:59Z INFO one second early
    2026-03-01T10:15:00Z WARN exactly at cutoff
    2026-03-01T10:20:00Z DEBUG at absolute cutoff
    2026-03-01T10:25:30Z ERROR late error
    2026-03-01T10:30:00Z INFO at now
    ''')


def build_correction():
    t = Task("multiturn-logtail-since")
    t.fx("go.mod", "module logtail\n\ngo 1.22\n")
    t.fx("README.md", d('''
        # logtail

        Prints log lines that match filters.

            go run ./cmd/logtail --level warn samples/app-log.txt

        Flags: `--level` (minimum level), `--now` (reference time, RFC 3339; default now).
        '''))
    t.fx("internal/logline/logline.go", LOGLINE_GO)
    t.fx("internal/logline/logline_test.go", LOGLINE_TEST)
    t.fx("cmd/logtail/main.go", MAIN_GO)
    t.fx("cmd/logtail/main_test.go", MAIN_TEST)
    t.fx("samples/app-log.txt", d('''
        2026-03-01T09:40:00Z INFO service started
        2026-03-01T09:58:12Z DEBUG cache warm
        2026-03-01T10:02:44Z WARN slow request /api/orders 1.8s
        2026-03-01T10:16:03Z ERROR upstream timeout payments
        2026-03-01T10:29:51Z INFO health ok
        '''))
    t.gr("_ho3_grade/cases.txt", CASES_LOG)
    argv = t.grade_script(GRADER_PRELUDE + d(r'''
        import subprocess

        exe = os.path.join("_ho3_grade", "logtail.exe" if os.name == "nt" else "logtail")
        b = subprocess.run(["go", "build", "-o", exe, "./cmd/logtail"], capture_output=True, text=True)
        if not check("go build ./cmd/logtail", b.returncode == 0, b.stderr[-800:]):
            finish()
        LOG = os.path.join("_ho3_grade", "cases.txt")
        with open(LOG, encoding="utf-8") as f:
            ALL = [l.rstrip("\n") for l in f if l.strip()]

        def run(*args):
            r = subprocess.run([exe] + list(args), capture_output=True, text=True, encoding="utf-8")
            return r.returncode, [l for l in r.stdout.splitlines() if l.strip()], r.stderr

        def lines_from(prefix):
            return [l for l in ALL if l[:20] >= prefix]

        NOW = "2026-03-01T10:30:00Z"
        code, out, err = run("--now", NOW, LOG)
        check("no --since: nothing filtered", code == 0 and out == ALL, (code, out, err[-300:]))
        code, out, err = run("--now", NOW, "--since", "15m", LOG)
        check("--since 15m keeps boundary entry", code == 0 and out == lines_from("2026-03-01T10:15:00Z"), (code, out, err[-300:]))
        code, out, err = run("--since", "30m", "--now", NOW, LOG)
        check("--since 30m (flag order swapped)", code == 0 and out == lines_from("2026-03-01T10:00:00Z"), (code, out, err[-300:]))
        code, out, err = run("--now", NOW, "--since", "1h", "--level", "warn", LOG)
        check("--since combines with --level", code == 0 and out == [l for l in ALL if " WARN " in l or " ERROR " in l], (code, out))
        code, out, err = run("--now", NOW, "--since", "2026-03-01T10:20:00Z", LOG)
        check("--since RFC3339 UTC keeps boundary", code == 0 and out == lines_from("2026-03-01T10:20:00Z"), (code, out, err[-300:]))
        code, out, err = run("--now", NOW, "--since", "2026-03-01T11:20:00+01:00", LOG)
        check("--since RFC3339 with offset", code == 0 and out == lines_from("2026-03-01T10:20:00Z"), (code, out, err[-300:]))
        code, out, err = run("--since", "2026-03-01T10:14:59Z", LOG)
        check("--since absolute works without --now", code == 0 and out == lines_from("2026-03-01T10:14:59Z"), (code, out, err[-300:]))
        code, out, err = run("--now", NOW, "--since", "last tuesday", LOG)
        check("invalid --since exits 2", code == 2, (code, out))
        check("invalid --since error mentions since", "since" in err.lower(), err[-300:])
        check("invalid --since prints nothing on stdout", out == [], out)
        finish()
        '''))
    main_solved = MAIN_SOLVED
    t.solve_files({"cmd/logtail/main.go": main_solved}, answer=(
        "`--since` now accepts a duration relative to `--now` (e.g. `15m`) or an RFC 3339 timestamp "
        "(with `Z` or an offset). Entries exactly at the cutoff are shown; an invalid value exits with "
        "status 2 and an error mentioning --since. `go test ./...` passes."))
    t.register(
        name="Two-turn conversation: add a --since flag, then a corrected specification",
        description=("Turn 1 asks for a relative --since filter; turn 2 corrects the spec (absolute RFC 3339 "
                     "timestamps too, inclusive boundary, exit status 2 on invalid input). Graded by building the "
                     "CLI and running it on hidden log cases."),
        category="multi-turn",
        tags=["multi-turn", "follow-up", "go", "cli"],
        difficulty="medium",
        prompt=turns(
            """Please add a `--since` option to logtail: `--since 15m` should show only the entries from the last 15 minutes (relative to `--now`). Without `--since` nothing is filtered. Keep `go test ./...` green.""",
            """One correction to what I asked before: `--since` must ALSO accept an absolute RFC 3339 timestamp (e.g. `--since 2026-03-01T10:00:00Z`, offsets like `+01:00` too), and an entry exactly at the cutoff must be shown, in both forms. An invalid `--since` value must make logtail exit with status 2 and print an error mentioning `--since` on stderr. The duration form must keep working."""),
        timeoutSeconds=1500,
        expectedChangedFiles=["cmd/logtail/main.go"],
        forbiddenChangedFiles=["go.mod", "samples/**"],
        strictDiff=True,
        allowedChangedFiles=["cmd/**", "internal/**", "README.md"],
        verificationCommands=[
            cmd("go-test", "go", "test", "-count=1", "./..."),
            cmd("cli-behaviour", *argv, weight=3),
        ],
    )


def build():
    build_undo()
    build_correction()
