"""Long-context tasks: exact report from a multi-file CSV ledger; eight-bug fix session."""
import csv
import io
from decimal import Decimal, ROUND_HALF_UP

from common import Task, d, cmd, go_filler, GO_MOD


# ------------------------------------------------------------ CSV ledger report
def ledger_report():
    t = Task("ledger-csv-report")
    rng = t.rng
    months = ["2026-%02d" % m for m in range(1, 7)]
    days = {"2026-01": 31, "2026-02": 28, "2026-03": 31, "2026-04": 30, "2026-05": 31, "2026-06": 30}
    currencies = ["EUR", "USD", "GBP", "PLN", "CHF", "SEK"]
    base = {"EUR": Decimal("1"), "USD": Decimal("0.9150"), "GBP": Decimal("1.1680"), "PLN": Decimal("0.2310"),
            "CHF": Decimal("1.0420"), "SEK": Decimal("0.0871")}
    rates = {}
    for m in months:
        for c in currencies:
            if c == "EUR":
                rates[(m, c)] = Decimal("1.0000")
            else:
                drift = Decimal(rng.randint(-150, 150)) / Decimal(10000)
                rates[(m, c)] = (base[c] * (1 + drift)).quantize(Decimal("0.0001"))
    regions = ["EU", "NA", "UK", "APAC", "LATAM"]
    accounts = {}
    for i in range(1, 121):
        acc = "ACC-%04d" % (i * 7)
        accounts[acc] = (rng.choice(regions), "closed" if rng.random() < 0.12 else "open")
    unknown_accounts = ["ACC-9001", "ACC-9002", "ACC-9003"]
    memos = ["Invoice {n}", "Invoice {n}, partial", "Refund for order {n}", "Subscription {n}", "Fees, {n} items",
             "Transfer \"{n}\"", "Adjustment", "Order {n}; split shipment"]
    statuses = ["settled"] * 16 + ["pending", "failed", "reversed"]
    rows_by_month = {m: [] for m in months}
    all_rows = []
    txn = 500000
    for m in months:
        for _ in range(rng.randint(1050, 1150)):
            txn += rng.randint(1, 3)
            acc = rng.choice(unknown_accounts) if rng.random() < 0.01 else rng.choice(list(accounts))
            cur = rng.choice(currencies)
            cents = rng.randint(100, 2500000)
            if rng.random() < 0.07:
                cents = -rng.randint(100, 300000)
            amount = Decimal(cents) / Decimal(100)
            row = ["T%d" % txn, "%s-%02d" % (m, rng.randint(1, days[m])), acc,
                   rng.choice(memos).format(n=rng.randint(10, 99999)), cur, "%.2f" % amount, rng.choice(statuses)]
            rows_by_month[m].append(row)
            all_rows.append(row)
    # duplicates: exact copies re-sent later in the same file or in a later month's file
    for _ in range(140):
        src = rng.choice(all_rows)
        src_month = src[1][:7]
        target = rng.choice([x for x in months if x >= src_month])
        lst = rows_by_month[target]
        lo = lst.index(src) + 1 if target == src_month else 0
        lst.insert(rng.randint(lo, len(lst)), list(src))
    for m in months:
        buf = io.StringIO()
        w = csv.writer(buf, lineterminator="\n")
        w.writerow(["txn_id", "date", "account", "memo", "currency", "amount", "status"])
        w.writerows(rows_by_month[m])
        t.fx("data/transactions/%s.csv" % m, buf.getvalue())
    buf = io.StringIO()
    w = csv.writer(buf, lineterminator="\n")
    w.writerow(["month", "currency", "rate_to_eur"])
    for m in months:
        for c in currencies:
            w.writerow([m, c, str(rates[(m, c)])])
    t.fx("data/fx_rates.csv", buf.getvalue())
    buf = io.StringIO()
    w = csv.writer(buf, lineterminator="\n")
    w.writerow(["account", "region", "status"])
    for acc in sorted(accounts):
        w.writerow([acc, accounts[acc][0], accounts[acc][1]])
    t.fx("data/accounts.csv", buf.getvalue())
    t.fx("README.md", d('''
        # ledger export 2026-H1

        * `data/transactions/YYYY-MM.csv`: bank export, one file per month, columns
          `txn_id,date,account,memo,currency,amount,status` (standard CSV quoting).
          The bank sometimes re-sends a transaction: the same row appears again later
          in the same file or in a later month's file.
        * `data/fx_rates.csv`: monthly conversion rate to EUR per currency.
        * `data/accounts.csv`: account master data (region, open/closed).
        '''))

    # expected report
    seen = set()
    dups = 0
    count = 0
    by_region, by_month, by_acc = {}, {}, {}
    for m in months:
        for row in rows_by_month[m]:
            tid, date, acc, _memo, cur, amount, status = row
            if tid in seen:
                dups += 1
                continue
            seen.add(tid)
            if status != "settled":
                continue
            region, astatus = accounts.get(acc, ("UNKNOWN", "open"))
            if astatus == "closed":
                continue
            eur = Decimal(amount) * rates[(date[:7], cur)]
            count += 1
            by_region[region] = by_region.get(region, Decimal(0)) + eur
            by_month[date[:7]] = by_month.get(date[:7], Decimal(0)) + eur
            by_acc[acc] = by_acc.get(acc, Decimal(0)) + eur

    def money(x):
        frac = (abs(x) * 100) % 1
        if abs(frac - Decimal("0.5")) < Decimal("0.001"):
            raise SystemExit("ledger: value %s too close to a rounding boundary, change the seed" % x)
        return str(x.quantize(Decimal("0.01"), rounding=ROUND_HALF_UP))

    top = sorted(by_acc.items(), key=lambda kv: (-kv[1], kv[0]))
    if top[4][1] == top[5][1]:
        raise SystemExit("ledger: tie in top 5")
    lines = ["# Ledger report 2026-H1", "", "settled transactions: %d" % count, "duplicate rows ignored: %d" % dups, "",
             "## EUR by region", ""]
    lines += ["%s: %s" % (r, money(v)) for r, v in sorted(by_region.items())]
    lines += ["", "## EUR by month", ""]
    lines += ["%s: %s" % (m, money(by_month[m])) for m in months]
    lines += ["", "## Top 5 accounts", ""]
    lines += ["%s: %s" % (a, money(v)) for a, v in top[:5]]
    expected = "\n".join(lines) + "\n"
    t.ref("REPORT.md", expected)
    n_rows = sum(len(v) for v in rows_by_month.values())
    t.register(
        name="Exact EUR report from a %d-row multi-file CSV ledger (dedupe, fx, closed accounts, quoted fields)" % n_rows,
        category="long-context", tags=["long-context", "csv", "analysis", "exact-output"], difficulty="hard",
        prompt=("Using the bank export described in README.md (data/transactions/*.csv, data/fx_rates.csv, "
                "data/accounts.csv), write REPORT.md with exactly this structure and no other text:\n\n"
                "```\n# Ledger report 2026-H1\n\nsettled transactions: <N>\nduplicate rows ignored: <D>\n\n"
                "## EUR by region\n\n<REGION>: <amount>\n\n## EUR by month\n\n<YYYY-MM>: <amount>\n\n"
                "## Top 5 accounts\n\n<ACCOUNT>: <amount>\n```\n\n"
                "Rules:\n"
                "- Process the monthly files in month order and rows in file order. A row whose txn_id already "
                "appeared earlier is a duplicate: ignore it (whatever its status) and count it in D.\n"
                "- Of the remaining rows, use only those with status `settled` whose account is not `closed` in "
                "data/accounts.csv. An account missing from data/accounts.csv belongs to region `UNKNOWN` and counts "
                "as open. N is the number of rows used.\n"
                "- Convert each amount to EUR with the rate for the month of the row's `date` and its currency "
                "(amount x rate_to_eur). Negative amounts (refunds) are included as they are.\n"
                "- Sum the exact (unrounded) EUR values per group and only then round each total half-up to exactly 2 "
                "decimals, written like `12345.60` or `-17.05` (no thousands separators, no currency sign).\n"
                "- Regions sorted alphabetically (only regions with at least one used row); months 2026-01 to "
                "2026-06 in order; top 5 accounts by EUR total, highest first.\n"
                "Do not modify anything under data/."),
        forbiddenChangedFiles=["data/**", "README.md"],
        expectedChangedFiles=["REPORT.md"],
        fileChecks=[{"id": "report-exact", "path": "REPORT.md", "equals": expected, "critical": True},
                    {"id": "report-counts", "path": "REPORT.md", "contains": ["settled transactions: %d\n" % count, "duplicate rows ignored: %d\n" % dups]}],
    )


# ------------------------------------------------------------ eight-bug fix session
MOD = "invoicer"

GO_FX = {
    "money/money.go": '''
        // Package money handles amounts in cents.
        package money

        import (
        	"fmt"
        	"strconv"
        	"strings"
        )

        // Cents is an amount in euro cents.
        type Cents int64

        // Format renders c with two decimals, e.g. "1234.50" or "-0.05".
        func Format(c Cents) string {
        	return fmt.Sprintf("%d.%02d", c/100, c%100)
        }

        // Parse reads "12", "12.5" or "12.50" (optionally negative) into cents.
        func Parse(s string) (Cents, error) {
        	s = strings.TrimSpace(s)
        	neg := strings.HasPrefix(s, "-")
        	s = strings.TrimPrefix(s, "-")
        	whole, frac, _ := strings.Cut(s, ".")
        	if len(frac) > 2 {
        		return 0, fmt.Errorf("money: too many decimals in %q", s)
        	}
        	for len(frac) < 2 {
        		frac += "0"
        	}
        	w, err := strconv.ParseInt(whole, 10, 64)
        	if err != nil {
        		return 0, fmt.Errorf("money: %w", err)
        	}
        	f, err := strconv.ParseInt(frac, 10, 64)
        	if err != nil {
        		return 0, fmt.Errorf("money: %w", err)
        	}
        	c := Cents(w*100 + f)
        	if neg {
        		c = -c
        	}
        	return c, nil
        }
        ''',
    "money/money_test.go": '''
        package money

        import "testing"

        func TestFormatParse(t *testing.T) {
        	if got := Format(123450); got != "1234.50" {
        		t.Fatalf("Format = %q", got)
        	}
        	if c, err := Parse("19.9"); err != nil || c != 1990 {
        		t.Fatalf("Parse = %d, %v", c, err)
        	}
        }
        ''',
    "tax/tax.go": '''
        // Package tax knows VAT rates (in basis points) and applies them.
        package tax

        import (
        	"fmt"

        	"invoicer/money"
        )

        var rates = map[string]int{"AT": 2000, "DE": 1900, "FR": 2000, "NL": 2100, "PL": 2300}

        // RateFor returns the VAT rate in basis points for an ISO country code.
        func RateFor(country string) (int, error) {
        	r, ok := rates[country]
        	if !ok {
        		return 0, fmt.Errorf("tax: unknown country %q", country)
        	}
        	return r, nil
        }

        // Tax returns net * bp / 10000 rounded half up to the cent.
        func Tax(net money.Cents, bp int) money.Cents {
        	return (net*money.Cents(bp) + 5000) / 10000
        }
        ''',
    "tax/tax_test.go": '''
        package tax

        import "testing"

        func TestRates(t *testing.T) {
        	if r, err := RateFor("DE"); err != nil || r != 1900 {
        		t.Fatalf("DE = %d, %v", r, err)
        	}
        	if _, err := RateFor("XX"); err == nil {
        		t.Fatal("XX should be unknown")
        	}
        	if got := Tax(10000, 1900); got != 1900 {
        		t.Fatalf("Tax = %d", got)
        	}
        }
        ''',
    "discount/discount.go": '''
        // Package discount implements quantity tiers.
        package discount

        import "invoicer/money"

        // PercentFor returns the tier discount for a total quantity:
        // 10 or more units 5%, 50 or more 10%, 100 or more 15%.
        func PercentFor(qty int) int {
        	switch {
        	case qty > 100:
        		return 15
        	case qty >= 50:
        		return 10
        	case qty >= 10:
        		return 5
        	}
        	return 0
        }

        // Amount returns pct percent of c, rounded half up to the cent.
        func Amount(c money.Cents, pct int) money.Cents {
        	return (c*money.Cents(pct) + 50) / 100
        }
        ''',
    "discount/discount_test.go": '''
        package discount

        import "testing"

        func TestTiers(t *testing.T) {
        	for qty, want := range map[int]int{1: 0, 10: 5, 49: 5, 50: 10, 150: 15} {
        		if got := PercentFor(qty); got != want {
        			t.Errorf("PercentFor(%d) = %d, want %d", qty, got, want)
        		}
        	}
        	if got := Amount(1999, 5); got != 100 {
        		t.Fatalf("Amount = %d", got)
        	}
        }
        ''',
    "dates/due.go": '''
        // Package dates computes payment due dates.
        package dates

        import "time"

        // DueDate returns issued + netDays. A due date on a Saturday or Sunday
        // moves to the following Monday.
        func DueDate(issued time.Time, netDays int) time.Time {
        	due := issued.AddDate(0, 0, netDays)
        	switch due.Weekday() {
        	case time.Saturday:
        		due = due.AddDate(0, 0, 2)
        	case time.Sunday:
        		due = due.AddDate(0, 0, 2)
        	}
        	return due
        }
        ''',
    "dates/due_test.go": '''
        package dates

        import (
        	"testing"
        	"time"
        )

        func TestDueDate(t *testing.T) {
        	issued := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
        	if got := DueDate(issued, 14); !got.Equal(issued.AddDate(0, 0, 14)) {
        		t.Fatalf("weekday due date moved: %v", got)
        	}
        	if got := DueDate(issued, 5); got.Weekday() != time.Monday { // Saturday -> Monday
        		t.Fatalf("Saturday not moved to Monday: %v", got)
        	}
        }
        ''',
    "ids/ids.go": '''
        // Package ids formats document numbers.
        package ids

        import "fmt"

        // InvoiceNumber formats a sequence number, e.g. InvoiceNumber(42) = "INV-000042".
        func InvoiceNumber(seq int) string {
        	return fmt.Sprintf("INV-%05d", seq)
        }

        // CreditNoteNumber formats a credit note number, e.g. "CN-0042".
        func CreditNoteNumber(seq int) string {
        	return fmt.Sprintf("CN-%04d", seq)
        }
        ''',
    "ids/ids_test.go": '''
        package ids

        import "testing"

        func TestCreditNote(t *testing.T) {
        	if got := CreditNoteNumber(42); got != "CN-0042" {
        		t.Fatal(got)
        	}
        }
        ''',
    "invoice/invoice.go": '''
        // Package invoice models invoices and computes their totals.
        package invoice

        import (
        	"time"

        	"invoicer/discount"
        	"invoicer/money"
        	"invoicer/tax"
        )

        // Line is one invoice line.
        type Line struct {
        	No   int
        	SKU  string
        	Desc string
        	Qty  int
        	Unit money.Cents
        }

        // Invoice is a customer invoice.
        type Invoice struct {
        	Number  string
        	Country string
        	Issued  time.Time
        	NetDays int
        	Lines   []Line
        }

        // Totals returns the net amount (after the quantity discount), the VAT
        // and the gross amount of inv.
        func Totals(inv Invoice) (net, vat, gross money.Cents, err error) {
        	var sub money.Cents
        	qty := 0
        	for _, l := range inv.Lines {
        		sub += money.Cents(l.Qty) * l.Unit
        		qty += l.Qty
        	}
        	rate, err := tax.RateFor(inv.Country)
        	if err != nil {
        		return 0, 0, 0, err
        	}
        	vat = tax.Tax(sub, rate)
        	net = sub - discount.Amount(sub, discount.PercentFor(qty))
        	return net, vat, net + vat, nil
        }
        ''',
    "invoice/invoice_test.go": '''
        package invoice

        import "testing"

        func TestTotalsNoDiscount(t *testing.T) {
        	inv := Invoice{Country: "DE", Lines: []Line{{No: 1, Qty: 2, Unit: 1000}}}
        	net, vat, gross, err := Totals(inv)
        	if err != nil || net != 2000 || vat != 380 || gross != 2380 {
        		t.Fatalf("Totals = %d %d %d %v", net, vat, gross, err)
        	}
        }
        ''',
    "csvio/csvio.go": '''
        // Package csvio imports invoice lines from CSV.
        package csvio

        import (
        	"bufio"
        	"fmt"
        	"io"
        	"strconv"
        	"strings"

        	"invoicer/invoice"
        	"invoicer/money"
        )

        // ReadLines reads "no,sku,desc,qty,unit" rows after a header row.
        func ReadLines(r io.Reader) ([]invoice.Line, error) {
        	sc := bufio.NewScanner(r)
        	var out []invoice.Line
        	n := 0
        	for sc.Scan() {
        		n++
        		if n == 1 || strings.TrimSpace(sc.Text()) == "" {
        			continue
        		}
        		f := strings.Split(sc.Text(), ",")
        		if len(f) != 5 {
        			return nil, fmt.Errorf("csvio: line %d: want 5 fields, got %d", n, len(f))
        		}
        		no, err := strconv.Atoi(f[0])
        		if err != nil {
        			return nil, fmt.Errorf("csvio: line %d: %w", n, err)
        		}
        		qty, err := strconv.Atoi(f[3])
        		if err != nil {
        			return nil, fmt.Errorf("csvio: line %d: %w", n, err)
        		}
        		unit, err := money.Parse(f[4])
        		if err != nil {
        			return nil, fmt.Errorf("csvio: line %d: %w", n, err)
        		}
        		out = append(out, invoice.Line{No: no, SKU: f[1], Desc: f[2], Qty: qty, Unit: unit})
        	}
        	return out, sc.Err()
        }
        ''',
    "csvio/csvio_test.go": '''
        package csvio

        import (
        	"strings"
        	"testing"
        )

        func TestReadLines(t *testing.T) {
        	lines, err := ReadLines(strings.NewReader("no,sku,desc,qty,unit\\n1,A-1,Bolt,10,0.25\\n\\n2,B-2,Nut,5,0.1\\n"))
        	if err != nil || len(lines) != 2 || lines[1].Unit != 10 || lines[0].Desc != "Bolt" {
        		t.Fatalf("ReadLines = %+v, %v", lines, err)
        	}
        }
        ''',
    "render/render.go": '''
        // Package render prints invoices as plain text.
        package render

        import (
        	"fmt"
        	"sort"
        	"strconv"
        	"strings"

        	"invoicer/dates"
        	"invoicer/invoice"
        	"invoicer/money"
        )

        // Text renders inv; lines are printed in line-number order.
        func Text(inv invoice.Invoice) (string, error) {
        	net, vat, gross, err := invoice.Totals(inv)
        	if err != nil {
        		return "", err
        	}
        	lines := append([]invoice.Line(nil), inv.Lines...)
        	sort.Slice(lines, func(i, j int) bool {
        		return strconv.Itoa(lines[i].No) < strconv.Itoa(lines[j].No)
        	})
        	var b strings.Builder
        	fmt.Fprintf(&b, "Invoice %s\\n", inv.Number)
        	fmt.Fprintf(&b, "Due %s\\n", dates.DueDate(inv.Issued, inv.NetDays).Format("2006-01-02"))
        	for _, l := range lines {
        		fmt.Fprintf(&b, "%3d %-8s %-20s %4d x %s\\n", l.No, l.SKU, l.Desc, l.Qty, money.Format(l.Unit))
        	}
        	fmt.Fprintf(&b, "Net %s\\nVAT %s\\nTotal %s\\n", money.Format(net), money.Format(vat), money.Format(gross))
        	return b.String(), nil
        }
        ''',
    "render/render_test.go": '''
        package render

        import (
        	"strings"
        	"testing"
        	"time"

        	"invoicer/invoice"
        )

        func TestText(t *testing.T) {
        	inv := invoice.Invoice{Number: "INV-1", Country: "DE", Issued: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), NetDays: 14,
        		Lines: []invoice.Line{{No: 2, SKU: "B", Qty: 1, Unit: 100}, {No: 1, SKU: "A", Qty: 1, Unit: 100}}}
        	out, err := Text(inv)
        	if err != nil {
        		t.Fatal(err)
        	}
        	if !strings.Contains(out, "Due 2026-03-16") || strings.Index(out, " A ") > strings.Index(out, " B ") {
        		t.Fatalf("unexpected output:\\n%s", out)
        	}
        }
        ''',
    "cmd/invoicer/main.go": '''
        // Command invoicer renders an invoice from a CSV of lines.
        package main

        import (
        	"fmt"
        	"os"
        	"strconv"
        	"time"

        	"invoicer/csvio"
        	"invoicer/ids"
        	"invoicer/invoice"
        	"invoicer/render"
        )

        func main() {
        	if len(os.Args) != 4 {
        		fmt.Fprintln(os.Stderr, "usage: invoicer <seq> <country> <lines.csv>")
        		os.Exit(2)
        	}
        	seq, err := strconv.Atoi(os.Args[1])
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(2)
        	}
        	f, err := os.Open(os.Args[3])
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(1)
        	}
        	defer f.Close()
        	lines, err := csvio.ReadLines(f)
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(1)
        	}
        	inv := invoice.Invoice{Number: ids.InvoiceNumber(seq), Country: os.Args[2], Issued: time.Now(), NetDays: 30, Lines: lines}
        	out, err := render.Text(inv)
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(1)
        	}
        	fmt.Print(out)
        }
        ''',
    "ISSUES.md": '''
        # Open issues

        Reported by customers and support; each needs a fix. Expected behaviour is
        what the issue says.

        ## #101 Credit amounts look broken

        A credit of -0.05 is printed as `0.-5` and -1.05 as `-1.-5`. Negative
        amounts must print with a leading minus and two decimals: `-0.05`,
        `-1.05`, `-1234.50`.

        ## #102 Import fails for lower-case country codes

        Country codes from the import arrive as `de`, ` pl ` and so on. The VAT
        lookup must ignore case and surrounding spaces. Unknown codes must still be
        an error.

        ## #103 100 units only get 10%

        Ordering exactly 100 units gives a 10% discount; the price list says 100 or
        more units get 15%.

        ## #104 Sunday due dates land on Tuesday

        An invoice whose due date falls on a Sunday is now due on Tuesday. It must
        move to the following Monday (Saturday already works).

        ## #105 VAT is charged on the undiscounted amount

        VAT must be computed on the net amount after the quantity discount:
        net = sum of lines - discount, VAT = tax on net, gross = net + VAT.
        Example: DE, 120 units at 10.00 -> net 1020.00, VAT 193.80, gross 1213.80.

        ## #106 Descriptions with commas cannot be imported

        `3,W-1,"Widget, large",2,19.99` fails with `want 5 fields, got 6`. The
        importer must accept standard CSV quoting (quoted fields may contain
        commas and doubled quotes).

        ## #107 Invoice numbers have the wrong width

        Invoice numbers must have six digits (INV-000042); they have five.
        Credit note numbers (CN-0042) are correct and must not change.

        ## #108 Line 10 is printed before line 2

        The text rendering must list lines in numeric order of their line number.
        ''',
    "README.md": '''
        # invoicer

        Small invoicing library: money, tax, discounts, due dates, CSV import and
        text rendering. `go test ./...`
        ''',
}

GO_REF = {
    "money/money.go": [("\treturn fmt.Sprintf(\"%d.%02d\", c/100, c%100)",
                        "\tsign := \"\"\n\tif c < 0 {\n\t\tsign = \"-\"\n\t\tc = -c\n\t}\n\treturn fmt.Sprintf(\"%s%d.%02d\", sign, c/100, c%100)")],
    "tax/tax.go": [('import (\n\t"fmt"\n', 'import (\n\t"fmt"\n\t"strings"\n'),
                   ("r, ok := rates[country]", "r, ok := rates[strings.ToUpper(strings.TrimSpace(country))]")],
    "discount/discount.go": [("case qty > 100:", "case qty >= 100:")],
    "dates/due.go": [("\tcase time.Sunday:\n\t\tdue = due.AddDate(0, 0, 2)", "\tcase time.Sunday:\n\t\tdue = due.AddDate(0, 0, 1)")],
    "ids/ids.go": [('"INV-%05d"', '"INV-%06d"')],
    "invoice/invoice.go": [("\tvat = tax.Tax(sub, rate)\n\tnet = sub - discount.Amount(sub, discount.PercentFor(qty))",
                            "\tnet = sub - discount.Amount(sub, discount.PercentFor(qty))\n\tvat = tax.Tax(net, rate)")],
    "csvio/csvio.go": [
        ('import (\n\t"bufio"\n\t"fmt"', 'import (\n\t"encoding/csv"\n\t"errors"\n\t"fmt"'),
        ('''	sc := bufio.NewScanner(r)
	var out []invoice.Line
	n := 0
	for sc.Scan() {
		n++
		if n == 1 || strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		f := strings.Split(sc.Text(), ",")
		if len(f) != 5 {''', '''	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	var out []invoice.Line
	first := true
	for {
		f, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csvio: %w", err)
		}
		n, _ := cr.FieldPos(0)
		if first {
			first = false
			continue
		}
		if len(f) == 1 && strings.TrimSpace(f[0]) == "" {
			continue
		}
		if len(f) != 5 {'''),
        ("\treturn out, sc.Err()", "\treturn out, nil"),
    ],
    "render/render.go": [('\t"sort"\n\t"strconv"\n', '\t"sort"\n'),
                         ("return strconv.Itoa(lines[i].No) < strconv.Itoa(lines[j].No)", "return lines[i].No < lines[j].No")],
}

GO_HIDDEN = {
    "money/zz_hidden_test.go": '''
        package money

        import "testing"

        func TestHiddenNegative(t *testing.T) {
        	for c, want := range map[Cents]string{-5: "-0.05", -105: "-1.05", -123450: "-1234.50", 0: "0.00", 7: "0.07", 100: "1.00"} {
        		if got := Format(c); got != want {
        			t.Errorf("Format(%d) = %q, want %q", c, got, want)
        		}
        	}
        }
        ''',
    "tax/zz_hidden_test.go": '''
        package tax

        import "testing"

        func TestHiddenCountryCase(t *testing.T) {
        	for _, c := range []string{"de", " pl ", "Nl", "AT"} {
        		if _, err := RateFor(c); err != nil {
        			t.Errorf("RateFor(%q): %v", c, err)
        		}
        	}
        	if r, _ := RateFor(" pl "); r != 2300 {
        		t.Errorf("pl = %d", r)
        	}
        	for _, c := range []string{"xx", "", "D E"} {
        		if _, err := RateFor(c); err == nil {
        			t.Errorf("RateFor(%q) should fail", c)
        		}
        	}
        }
        ''',
    "discount/zz_hidden_test.go": '''
        package discount

        import "testing"

        func TestHiddenBoundary(t *testing.T) {
        	for qty, want := range map[int]int{9: 0, 99: 10, 100: 15, 101: 15} {
        		if got := PercentFor(qty); got != want {
        			t.Errorf("PercentFor(%d) = %d, want %d", qty, got, want)
        		}
        	}
        }
        ''',
    "dates/zz_hidden_test.go": '''
        package dates

        import (
        	"testing"
        	"time"
        )

        func TestHiddenSunday(t *testing.T) {
        	issued := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // Monday
        	if got, want := DueDate(issued, 6), time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
        		t.Errorf("Sunday due date = %v, want %v", got, want)
        	}
        	if got, want := DueDate(issued, 5), time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
        		t.Errorf("Saturday due date = %v, want %v", got, want)
        	}
        }
        ''',
    "ids/zz_hidden_test.go": '''
        package ids

        import "testing"

        func TestHiddenWidth(t *testing.T) {
        	if got := InvoiceNumber(42); got != "INV-000042" {
        		t.Errorf("InvoiceNumber(42) = %q", got)
        	}
        	if got := InvoiceNumber(1234567); got != "INV-1234567" {
        		t.Errorf("InvoiceNumber(1234567) = %q", got)
        	}
        	if got := CreditNoteNumber(42); got != "CN-0042" {
        		t.Errorf("CreditNoteNumber(42) = %q", got)
        	}
        }
        ''',
    "invoice/zz_hidden_test.go": '''
        package invoice

        import "testing"

        func TestHiddenDiscountBeforeVAT(t *testing.T) {
        	inv := Invoice{Country: "DE", Lines: []Line{{No: 1, Qty: 60, Unit: 1000}, {No: 2, Qty: 60, Unit: 1000}}}
        	net, vat, gross, err := Totals(inv)
        	if err != nil || net != 102000 || vat != 19380 || gross != 121380 {
        		t.Fatalf("Totals = %d %d %d %v, want 102000 19380 121380", net, vat, gross, err)
        	}
        	inv = Invoice{Country: "PL", Lines: []Line{{No: 1, Qty: 12, Unit: 333}}}
        	net, vat, gross, err = Totals(inv)
        	if err != nil || net != 3796 || vat != 873 || gross != 4669 {
        		t.Fatalf("Totals = %d %d %d %v, want 3796 873 4669", net, vat, gross, err)
        	}
        }
        ''',
    "csvio/zz_hidden_test.go": '''
        package csvio

        import (
        	"strings"
        	"testing"
        )

        func TestHiddenQuoted(t *testing.T) {
        	in := "no,sku,desc,qty,unit\\n3,W-1,\\"Widget, large\\",2,19.99\\n4,Q-9,\\"The \\"\\"best\\"\\" nut\\",1,0.5\\n"
        	lines, err := ReadLines(strings.NewReader(in))
        	if err != nil || len(lines) != 2 {
        		t.Fatalf("ReadLines = %+v, %v", lines, err)
        	}
        	if lines[0].Desc != "Widget, large" || lines[0].Unit != 1999 || lines[0].Qty != 2 || lines[0].No != 3 {
        		t.Fatalf("line 0 = %+v", lines[0])
        	}
        	if lines[1].Desc != `The "best" nut` || lines[1].Unit != 50 {
        		t.Fatalf("line 1 = %+v", lines[1])
        	}
        	if _, err := ReadLines(strings.NewReader("no,sku,desc,qty,unit\\n1,A,B,x,1\\n")); err == nil {
        		t.Fatal("bad qty must still fail")
        	}
        }
        ''',
    "render/zz_hidden_test.go": '''
        package render

        import (
        	"strings"
        	"testing"
        	"time"

        	"invoicer/invoice"
        )

        func TestHiddenNumericOrder(t *testing.T) {
        	var ls []invoice.Line
        	for _, n := range []int{10, 2, 1, 11, 3} {
        		ls = append(ls, invoice.Line{No: n, SKU: "S" + string(rune('A'+n)), Qty: 1, Unit: 100})
        	}
        	out, err := Text(invoice.Invoice{Number: "INV-000001", Country: "DE", Issued: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), NetDays: 13, Lines: ls})
        	if err != nil {
        		t.Fatal(err)
        	}
        	prev := -1
        	for _, n := range []int{1, 2, 3, 10, 11} {
        		i := strings.Index(out, " S"+string(rune('A'+n))+" ")
        		if i < prev {
        			t.Fatalf("line %d out of order:\\n%s", n, out)
        		}
        		prev = i
        	}
        }
        ''',
}

PKGS = ["money", "tax", "discount", "dates", "ids", "invoice", "csvio", "render"]


def bug_session():
    t = Task("go-invoice-bugs")
    t.fx("go.mod", GO_MOD.format(mod=MOD))
    for rel, body in GO_FX.items():
        t.fx(rel, d(body))
    for pkg in ["internal/customers", "internal/reminders", "internal/archive", "internal/exports", "internal/audit"]:
        go_filler(t, MOD, pkg, count=4)
    for rel, reps in GO_REF.items():
        t.ref_from_fx(rel, *reps)
    for rel, body in GO_HIDDEN.items():
        t.gr(rel, d(body))
    checks = [cmd("go-vet", "go", "vet", "./...")]
    checks += [cmd("issue-" + p, "go", "test", "-count=1", "./%s/" % p, optional=True) for p in PKGS]
    t.register(
        name="Fix eight independent bugs from ISSUES.md across eight packages (reduced context window)",
        category="long-context", tags=["go", "long", "debugging", "multi-fix", "compaction"], difficulty="hard",
        prompt=("ISSUES.md lists eight open bug reports (#101-#108) for this Go module. Fix all of them so the "
                "behaviour matches what each issue describes. Keep exported function signatures unchanged, do not "
                "edit ISSUES.md or the existing *_test.go files (you may add new test files), and make sure "
                "`go vet ./...` and `go test ./...` pass. At the end, list each issue number with the file you "
                "changed for it."),
        timeoutSeconds=2400, agentConfig="context_window = 24576\n",
        forbiddenChangedFiles=["ISSUES.md", "go.mod", "internal/**"] + ["%s/%s_test.go" % (p, n) for p, n in [
            ("money", "money"), ("tax", "tax"), ("discount", "discount"), ("dates", "due"), ("ids", "ids"),
            ("invoice", "invoice"), ("csvio", "csvio"), ("render", "render")]],
        expectedChangedFiles=sorted(GO_REF),
        verificationCommands=checks,
    )


def build():
    ledger_report()
    bug_session()
