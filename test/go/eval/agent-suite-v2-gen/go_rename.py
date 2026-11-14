"""v2-go-rename-api: rename money.ParseAmount -> money.Parse across a 45-file repo."""
from common import Task, d, cmd, go_filler, GO_MOD

MOD = "shopcore"


def build():
    t = Task("go-rename-api")
    t.fx("go.mod", GO_MOD.format(mod=MOD))
    money = d('''
        // Package money handles amounts in minor units (cents).
        package money

        import (
        	"errors"
        	"fmt"
        	"strconv"
        	"strings"
        )

        // Amount is a value in minor units (cents).
        type Amount int64

        // ErrEmpty is returned for blank input. The message is part of the
        // shopctl CLI contract (scripts match on it), keep it byte-identical.
        var ErrEmpty = errors.New("money.ParseAmount: empty input")

        // ParseAmount parses "12.34", "12", "-0.5" or "1,234.50" into cents.
        func ParseAmount(s string) (Amount, error) {
        	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
        	if s == "" {
        		return 0, ErrEmpty
        	}
        	neg := strings.HasPrefix(s, "-")
        	s = strings.TrimPrefix(s, "-")
        	whole, frac, _ := strings.Cut(s, ".")
        	if len(frac) > 2 {
        		return 0, fmt.Errorf("money.ParseAmount: too many decimals in %q", s)
        	}
        	for len(frac) < 2 {
        		frac += "0"
        	}
        	var w int64
        	if whole != "" {
        		v, err := strconv.ParseInt(whole, 10, 64)
        		if err != nil {
        			return 0, fmt.Errorf("money.ParseAmount: bad number %q", s)
        		}
        		w = v
        	}
        	f, err := strconv.ParseInt(frac, 10, 64)
        	if err != nil {
        		return 0, fmt.Errorf("money.ParseAmount: bad number %q", s)
        	}
        	a := Amount(w*100 + f)
        	if neg {
        		a = -a
        	}
        	return a, nil
        }

        // ParseAmountStrict is like ParseAmount but rejects thousands
        // separators and requires exactly two decimals.
        func ParseAmountStrict(s string) (Amount, error) {
        	if strings.Contains(s, ",") {
        		return 0, fmt.Errorf("money.ParseAmountStrict: separator in %q", s)
        	}
        	if i := strings.IndexByte(s, '.'); i < 0 || len(s)-i-1 != 2 {
        		return 0, fmt.Errorf("money.ParseAmountStrict: need two decimals in %q", s)
        	}
        	return ParseAmount(s)
        }

        // String renders the amount as "-12.34".
        func (a Amount) String() string {
        	sign := ""
        	if a < 0 {
        		sign, a = "-", -a
        	}
        	return fmt.Sprintf("%s%d.%02d", sign, a/100, a%100)
        }
        ''')
    t.fx("internal/money/money.go", money)
    money_test = d('''
        package money

        import "testing"

        func TestParseAmount(t *testing.T) {
        	cases := map[string]Amount{"12.34": 1234, "12": 1200, "-0.5": -50, "1,234.50": 123450}
        	for in, want := range cases {
        		got, err := ParseAmount(in)
        		if err != nil || got != want {
        			t.Errorf("ParseAmount(%q) = %v, %v; want %v", in, got, err, want)
        		}
        	}
        	if _, err := ParseAmount("  "); err == nil || err.Error() != "money.ParseAmount: empty input" {
        		t.Errorf("empty input error = %v", err)
        	}
        }

        func TestParseAmountStrict(t *testing.T) {
        	if _, err := ParseAmountStrict("1,000.00"); err == nil {
        		t.Error("separator accepted")
        	}
        	if got, err := ParseAmountStrict("3.05"); err != nil || got != 305 {
        		t.Errorf("ParseAmountStrict = %v, %v", got, err)
        	}
        }
        ''')
    t.fx("internal/money/money_test.go", money_test)

    catalog = d('''
        // Package catalog loads product rows.
        package catalog

        import (
        	"fmt"

        	"shopcore/internal/money"
        )

        // Row is one raw catalog line.
        type Row struct {
        	SKU   string
        	Price string
        	Sale  string
        }

        // Product is a parsed catalog entry.
        type Product struct {
        	SKU   string
        	Price money.Amount
        	Sale  money.Amount
        }

        // Load parses rows; Sale is optional.
        func Load(rows []Row) ([]Product, error) {
        	out := make([]Product, 0, len(rows))
        	for _, r := range rows {
        		p, err := money.ParseAmount(r.Price)
        		if err != nil {
        			return nil, fmt.Errorf("sku %s: %w", r.SKU, err)
        		}
        		prod := Product{SKU: r.SKU, Price: p}
        		if r.Sale != "" {
        			s, err := money.ParseAmount(r.Sale)
        			if err != nil {
        				return nil, fmt.Errorf("sku %s sale: %w", r.SKU, err)
        			}
        			prod.Sale = s
        		}
        		out = append(out, prod)
        	}
        	return out, nil
        }
        ''')
    t.fx("internal/catalog/catalog.go", catalog)
    importer = d('''
        package catalog

        import (
        	"strings"

        	"shopcore/internal/money"
        )

        // Importer reads supplier feeds whose prices carry a currency prefix.
        type Importer struct {
        	Currency string
        }

        // ParseAmount strips the importer's currency prefix and parses the
        // rest. It is the supplier-feed entry point and keeps its name.
        func (im *Importer) ParseAmount(field string) (money.Amount, error) {
        	field = strings.TrimSpace(field)
        	field = strings.TrimPrefix(field, im.Currency)
        	return money.ParseAmount(field)
        }

        // ParseStrictFeed is used for the audited feed, which must have two decimals.
        func (im *Importer) ParseStrictFeed(field string) (money.Amount, error) {
        	return money.ParseAmountStrict(strings.TrimPrefix(strings.TrimSpace(field), im.Currency))
        }
        ''')
    t.fx("internal/catalog/importer.go", importer)
    t.fx("internal/catalog/catalog_test.go", d('''
        package catalog

        import "testing"

        func TestLoad(t *testing.T) {
        	ps, err := Load([]Row{{SKU: "a", Price: "1.50"}, {SKU: "b", Price: "2", Sale: "1.75"}})
        	if err != nil || len(ps) != 2 || ps[1].Sale != 175 {
        		t.Fatalf("Load = %+v, %v", ps, err)
        	}
        }

        func TestImporter(t *testing.T) {
        	im := &Importer{Currency: "$"}
        	if a, err := im.ParseAmount("$4.20"); err != nil || a != 420 {
        		t.Fatalf("ParseAmount = %v, %v", a, err)
        	}
        	if _, err := im.ParseStrictFeed("$4.2"); err == nil {
        		t.Fatal("strict feed accepted one decimal")
        	}
        }
        '''))

    orders = {
        "order.go": d('''
            // Package orders computes order totals.
            package orders

            import "shopcore/internal/money"

            // Line is an order line as submitted by the storefront.
            type Line struct {
            	SKU      string
            	Qty      int
            	UnitText string
            }

            // Order groups lines.
            type Order struct {
            	ID    string
            	Lines []Line
            	Note  string
            }

            // UnitPrice parses the line's unit price.
            func (l Line) UnitPrice() (money.Amount, error) {
            	return money.ParseAmount(l.UnitText)
            }
            '''),
        "totals.go": d('''
            package orders

            import "shopcore/internal/money"

            // Total sums qty*unit over all lines.
            func Total(o Order) (money.Amount, error) {
            	var sum money.Amount
            	for _, l := range o.Lines {
            		u, err := l.UnitPrice()
            		if err != nil {
            			return 0, err
            		}
            		sum += u * money.Amount(l.Qty)
            	}
            	return sum, nil
            }

            // ApplyCoupon subtracts a flat coupon value given as text.
            func ApplyCoupon(total money.Amount, coupon string) (money.Amount, error) {
            	c, err := money.ParseAmount(coupon)
            	if err != nil {
            		return 0, err
            	}
            	if c > total {
            		return 0, nil
            	}
            	return total - c, nil
            }
            '''),
        "refund.go": d('''
            package orders

            import (
            	"fmt"

            	"shopcore/internal/money"
            )

            // Refund validates a refund request against the paid amount.
            func Refund(paid money.Amount, requested string) (money.Amount, error) {
            	r, err := money.ParseAmountStrict(requested)
            	if err != nil {
            		return 0, err
            	}
            	if r > paid {
            		return 0, fmt.Errorf("refund %s exceeds paid %s", r, paid)
            	}
            	return r, nil
            }

            // PartialRefund accepts loose input (e.g. from support tooling).
            func PartialRefund(paid money.Amount, requested string) (money.Amount, error) {
            	r, err := money.ParseAmount(requested)
            	if err != nil {
            		return 0, err
            	}
            	if r > paid {
            		r = paid
            	}
            	return r, nil
            }
            '''),
        "orders_test.go": d('''
            package orders

            import "testing"

            func TestTotal(t *testing.T) {
            	o := Order{Lines: []Line{{Qty: 2, UnitText: "1.25"}, {Qty: 1, UnitText: "10"}}}
            	if got, err := Total(o); err != nil || got != 1250 {
            		t.Fatalf("Total = %v, %v", got, err)
            	}
            	if got, _ := ApplyCoupon(1000, "2.50"); got != 750 {
            		t.Fatalf("ApplyCoupon = %v", got)
            	}
            	if _, err := Refund(100, "2.00"); err == nil {
            		t.Fatal("refund above paid accepted")
            	}
            	if got, _ := PartialRefund(100, "2"); got != 100 {
            		t.Fatalf("PartialRefund = %v", got)
            	}
            }
            '''),
    }
    for name, body in orders.items():
        t.fx("internal/orders/" + name, body)
    t.fx("internal/reports/daily.go", d('''
        // Package reports renders sales reports.
        package reports

        import (
        	"fmt"
        	"strings"

        	"shopcore/internal/money"
        )

        // DailyLine is "sku;amount".
        func SumDaily(lines []string) (money.Amount, error) {
        	var sum money.Amount
        	for i, l := range lines {
        		_, amt, ok := strings.Cut(l, ";")
        		if !ok {
        			return 0, fmt.Errorf("line %d: no amount", i+1)
        		}
        		a, err := money.ParseAmount(amt)
        		if err != nil {
        			return 0, fmt.Errorf("line %d: %w", i+1, err)
        		}
        		sum += a
        	}
        	return sum, nil
        }
        '''))
    t.fx("internal/reports/csv.go", d('''
        package reports

        import (
        	"encoding/csv"
        	"io"

        	"shopcore/internal/money"
        )

        // SumCSV sums column col of a CSV stream (header skipped).
        func SumCSV(r io.Reader, col int) (money.Amount, error) {
        	rows, err := csv.NewReader(r).ReadAll()
        	if err != nil {
        		return 0, err
        	}
        	var sum money.Amount
        	for i, row := range rows {
        		if i == 0 || col >= len(row) {
        			continue
        		}
        		a, err := money.ParseAmount(row[col])
        		if err != nil {
        			return 0, err
        		}
        		sum += a
        	}
        	return sum, nil
        }
        '''))
    t.fx("internal/reports/reports_test.go", d('''
        package reports

        import (
        	"strings"
        	"testing"
        )

        func TestSums(t *testing.T) {
        	if got, err := SumDaily([]string{"a;1.00", "b;2.5"}); err != nil || got != 350 {
        		t.Fatalf("SumDaily = %v, %v", got, err)
        	}
        	if got, err := SumCSV(strings.NewReader("sku,amt\\na,\\"1,000.00\\"\\n"), 1); err != nil || got != 100000 {
        		t.Fatalf("SumCSV = %v, %v", got, err)
        	}
        }
        '''))
    t.fx("cmd/shopctl/main.go", d('''
        // Command shopctl is a small admin CLI.
        package main

        import (
        	"fmt"
        	"os"

        	"shopcore/internal/money"
        )

        func main() {
        	if len(os.Args) != 3 || os.Args[1] != "parse" {
        		fmt.Fprintln(os.Stderr, "usage: shopctl parse <amount>")
        		os.Exit(2)
        	}
        	a, err := money.ParseAmount(os.Args[2])
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(1)
        	}
        	fmt.Println(a)
        }
        '''))
    api_doc = d('''
        # shopcore API

        ## money

        - `money.ParseAmount(s string) (Amount, error)`: loose parser, accepts
          `1,234.5`, `12`, `-0.5`.
        - `money.ParseAmountStrict(s string) (Amount, error)`: exactly two
          decimals, no separators.
        - `Amount.String()`: `-12.34` style rendering.

        Error messages (`money.ParseAmount: empty input`, ...) are matched by
        operator scripts and must not change.

        ## catalog

        - `(*Importer).ParseAmount(field)`: supplier feeds with a currency prefix.
        ''')
    t.fx("docs/API.md", api_doc)
    t.fx("README.md", "# shopcore\n\nStorefront back office. `go test ./...` runs everything; see docs/API.md.\n")
    for pkg in ["shipping", "customers", "promo", "tax", "audit", "inventory", "loyalty"]:
        go_filler(t, MOD, "internal/" + pkg, count=4)

    # ---- reference
    def ren(text):
        return (text.replace("money.ParseAmountStrict", "money.\x00STRICT")
                .replace("money.ParseAmount(", "money.Parse(")
                .replace("money.\x00STRICT", "money.ParseAmountStrict"))
    ref_money = (money
                 .replace("// ParseAmount parses", "// Parse parses")
                 .replace("func ParseAmount(s string)", "func Parse(s string)")
                 .replace("// ParseAmountStrict is like ParseAmount but", "// ParseAmountStrict is like Parse but")
                 .replace("\treturn ParseAmount(s)\n", "\treturn Parse(s)\n"))
    t.ref("internal/money/money.go", ref_money)
    t.ref("internal/money/money_test.go", money_test.replace("ParseAmount(in)", "Parse(in)")
          .replace('ParseAmount(%q) = ', 'Parse(%q) = ').replace('ParseAmount("  ")', 'Parse("  ")')
          .replace("func TestParseAmount(t", "func TestParse(t"))
    for rel in ["internal/catalog/catalog.go", "internal/catalog/importer.go", "internal/orders/order.go",
                "internal/orders/totals.go", "internal/orders/refund.go", "internal/reports/daily.go",
                "internal/reports/csv.go", "cmd/shopctl/main.go"]:
        t.ref(rel, ren(t.read_fx(rel)))
    t.ref("docs/API.md", api_doc.replace("`money.ParseAmount(s string)", "`money.Parse(s string)"))

    # ---- hidden tests
    t.gr("internal/money/zz_hidden_test.go", d('''
        package money

        import "testing"

        func TestHiddenRenamedAPI(t *testing.T) {
        	if a, err := Parse("1,234.5"); err != nil || a != 123450 {
        		t.Fatalf("Parse = %v, %v", a, err)
        	}
        	if _, err := Parse(""); err == nil || err.Error() != "money.ParseAmount: empty input" {
        		t.Fatalf("empty error text changed: %v", err)
        	}
        	if _, err := Parse("1.234"); err == nil || err.Error() != `money.ParseAmount: too many decimals in "1.234"` {
        		t.Fatalf("decimals error text changed: %v", err)
        	}
        	if _, err := ParseAmountStrict("1,0.00"); err == nil || err.Error() != `money.ParseAmountStrict: separator in "1,0.00"` {
        		t.Fatalf("strict error text changed: %v", err)
        	}
        	if a, err := ParseAmountStrict("7.07"); err != nil || a != 707 {
        		t.Fatalf("ParseAmountStrict = %v, %v", a, err)
        	}
        }
        '''))
    t.gr("internal/catalog/zz_hidden_test.go", d('''
        package catalog

        import "testing"

        func TestHiddenImporterKeepsMethodName(t *testing.T) {
        	im := &Importer{Currency: "EUR"}
        	if a, err := im.ParseAmount("EUR 3.10"); err != nil || a != 310 {
        		t.Fatalf("Importer.ParseAmount = %v, %v", a, err)
        	}
        }
        '''))
    t.register(
        name="Rename an API across a 45-file Go repo (with a same-named method and error-text traps)",
        category="refactor", tags=["go", "rename", "multi-file"], difficulty="hard",
        prompt=("Rename the package-level function `money.ParseAmount` to `money.Parse` across the whole repository "
                "(all callers, tests and docs/API.md). Keep `money.ParseAmountStrict` as it is. Error message strings "
                "are a CLI contract and must stay byte-identical. Other types' methods that happen to be called "
                "ParseAmount are not part of this rename. `go vet ./...` and `go test ./...` must pass at the end."),
        strictDiff=True,
        allowedChangedFiles=["internal/**/*.go", "cmd/**/*.go", "docs/API.md", "CHANGELOG.md"],
        expectedChangedFiles=["internal/money/money.go", "docs/API.md"],
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./...")],
        fileChecks=[
            {"id": "no-old-callers", "path": "**/*.go", "notRegex": [r"money\.ParseAmount\(", r"\bParseStrict\b", r"\) Parse\(field"], "critical": True},
            {"id": "error-text-kept", "path": "internal/money/money.go", "contains": ['"money.ParseAmount: empty input"', "money.ParseAmountStrict: separator in"], "critical": True},
            {"id": "docs-updated", "path": "docs/API.md", "contains": ["money.Parse(s string)", "money.ParseAmountStrict(s string)", "(*Importer).ParseAmount(field)"], "notContains": ["money.ParseAmount(s string)"]},
        ],
    )
