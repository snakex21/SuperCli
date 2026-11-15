"""Long-context Go debugging: a billing test fails with an error raised two packages away from the
real cause (a model-name normaliser in a third package). Many packages and a ~1k-row CSV fixture."""
import datetime

from common import Task, d, cmd, go_filler, GO_MOD

MOD = "meterd"

DIGITS = {"EM5": 5, "EM6": 6, "EMX": 7}
TIERS = [(300, 62), (1000, 71), (None, 85)]  # (up to kWh, grosze per kWh)
FIXED_FEE = 1850


def _norm(model):
    return "".join(ch.upper() for ch in model if ch.isalnum())


def _bill(kwh):
    amount, prev = FIXED_FEE, 0
    for upto, rate in TIERS:
        top = kwh if upto is None else min(kwh, upto)
        if top > prev:
            amount += (top - prev) * rate
            prev = top
    return amount


def build():
    t = Task("go-meter-rollover")
    rng = t.rng
    t.fx("go.mod", GO_MOD.format(mod=MOD))

    # ---------------------------------------------------------------- data
    models = ["EM-5", "em5", "EM 5", "EMX", "emx", "EM-6", "Em6", "em-6"]
    meters = []  # (meter, customer, model, start)
    for i in range(30):
        model = models[i % len(models)]
        start = rng.randint(1000, 60000)
        meters.append(("M-%04d" % (2100 + i * 7), "C-%04d" % (10 + i * 3), model, start))
    # two meters roll over during March: a 5-digit one (handled fine today) and a 6-digit one (broken today)
    roll5, roll6 = 2, 5  # indexes: "EM 5" and "EM-6"
    assert _norm(meters[roll5][2]) == "EM5" and _norm(meters[roll6][2]) == "EM6"
    meters[roll5] = meters[roll5][:3] + (99_000 + rng.randint(500, 700),)
    meters[roll6] = meters[roll6][:3] + (999_000 + rng.randint(500, 700),)
    rows = []
    usage = {}
    base = datetime.datetime(2027, 3, 1, 6, 0, 0)
    for idx, (mid, cust, model, start) in enumerate(meters):
        mod = 10 ** DIGITS[_norm(model)]
        v = start
        total = 0
        for day in range(32):
            ts = base + datetime.timedelta(days=day, minutes=rng.randint(-20, 20))
            rows.append((mid, ts.strftime("%Y-%m-%dT%H:%M:%SZ"), v % mod))
            step = rng.randint(4, 26) if day < 31 else 0
            if idx == roll6 and day < 31:
                step = rng.randint(30, 60)
            v += step
            if day < 31:
                total += step
        usage[cust] = usage.get(cust, 0) + total
    rng.shuffle(rows)
    t.fx("testdata/march/readings.csv", "meter,at,value\n" + "".join("%s,%s,%d\n" % r for r in rows))
    t.fx("testdata/march/meters.csv", "meter,customer,model\n" + "".join("%s,%s,%s\n" % m[:3] for m in meters))
    bills = {c: _bill(k) for c, k in usage.items()}
    roll6_cust, roll5_cust = meters[roll6][1], meters[roll5][1]
    plain_cust = meters[11][1]

    # ---------------------------------------------------------------- meter (the cause)
    t.fx("meter/doc.go", d('''
        // Package meter knows the electricity meter models in the field.
        //
        // Every model has a register with a fixed number of digits; when the
        // register passes its maximum it wraps around to zero. Model names come
        // from installer paperwork and are matched case-insensitively, ignoring
        // punctuation and spaces: "em-6", "EM6" and "Em 6" are the same model.
        // Unknown models are assumed to have 5 digits, the most common register.
        package meter
        '''))
    t.fx("meter/registry.go", d('''
        package meter

        // digits maps a normalised model name to its register width.
        var digits = map[string]int{
            "EM5": 5,
            "EM6": 6,
            "EMX": 7,
        }

        // DefaultDigits is the register width assumed for unknown models.
        const DefaultDigits = 5

        // Digits returns the register width of model.
        func Digits(model string) int {
            if n, ok := digits[normalizeModel(model)]; ok {
                return n
            }
            return DefaultDigits
        }

        // Modulus returns the value at which the register of model wraps to zero.
        func Modulus(model string) int64 {
            m := int64(1)
            for i := 0; i < Digits(model); i++ {
                m *= 10
            }
            return m
        }

        // Known reports whether model is a known model.
        func Known(model string) bool {
            _, ok := digits[normalizeModel(model)]
            return ok
        }
        '''))
    normalize = d('''
        package meter

        import (
            "strings"
            "unicode"
        )

        // normalizeModel upper-cases a model name and drops separators such as
        // dashes, dots and spaces.
        func normalizeModel(model string) string {
            return strings.Map(func(r rune) rune {
                if unicode.IsLetter(r) {
                    return unicode.ToUpper(r)
                }
                return -1
            }, model)
        }
        ''')
    t.fx("meter/normalize.go", normalize)
    t.fx("meter/registry_test.go", d('''
        package meter

        import "testing"

        func TestDigits(t *testing.T) {
            cases := map[string]int{"EM-5": 5, "em5": 5, "EMX": 7, "emx": 7, "XR-200": 5}
            for model, want := range cases {
                if got := Digits(model); got != want {
                    t.Errorf("Digits(%q) = %d, want %d", model, got, want)
                }
            }
        }

        func TestModulus(t *testing.T) {
            if got := Modulus("EM-5"); got != 100000 {
                t.Fatalf("Modulus = %d", got)
            }
        }
        '''))

    # ---------------------------------------------------------------- readings (where the error is raised)
    t.fx("readings/reading.go", d('''
        // Package readings holds register readings and turns them into consumption.
        package readings

        import (
            "sort"
            "time"
        )

        // Reading is one register value of a meter at a point in time (kWh).
        type Reading struct {
            Meter string
            At    time.Time
            Value int64
        }

        // SortByTime orders readings chronologically (stable for equal times).
        func SortByTime(rs []Reading) {
            sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
        }

        // ByMeter groups readings by meter id.
        func ByMeter(rs []Reading) map[string][]Reading {
            out := map[string][]Reading{}
            for _, r := range rs {
                out[r.Meter] = append(out[r.Meter], r)
            }
            return out
        }
        '''))
    t.fx("readings/deltas.go", d('''
        package readings

        import (
            "fmt"
            "time"
        )

        // Delta is the consumption between two consecutive readings.
        type Delta struct {
            From, To time.Time
            KWh      int64
        }

        // Deltas returns the consumption between consecutive readings of one
        // meter (rs must be sorted by time). A register that passes its maximum
        // wraps to zero, so a decrease means exactly one wrap-around: modulus is
        // added once. Anything still negative is corrupt data.
        func Deltas(rs []Reading, modulus int64) ([]Delta, error) {
            var out []Delta
            for i := 1; i < len(rs); i++ {
                kwh := rs[i].Value - rs[i-1].Value
                if kwh < 0 {
                    kwh += modulus
                }
                if kwh < 0 {
                    return nil, fmt.Errorf("negative delta %d at %s", kwh, rs[i].At.Format(time.RFC3339))
                }
                out = append(out, Delta{From: rs[i-1].At, To: rs[i].At, KWh: kwh})
            }
            return out, nil
        }

        // Sum adds up the consumption of ds.
        func Sum(ds []Delta) int64 {
            var n int64
            for _, d := range ds {
                n += d.KWh
            }
            return n
        }
        '''))
    t.fx("readings/deltas_test.go", d('''
        package readings

        import (
            "testing"
            "time"
        )

        func at(day int) time.Time { return time.Date(2027, 1, day, 0, 0, 0, 0, time.UTC) }

        func TestDeltasWrap(t *testing.T) {
            rs := []Reading{{Value: 99990, At: at(1)}, {Value: 5, At: at(2)}, {Value: 20, At: at(3)}}
            ds, err := Deltas(rs, 100000)
            if err != nil {
                t.Fatal(err)
            }
            if Sum(ds) != 30 {
                t.Fatalf("Sum = %d", Sum(ds))
            }
        }

        func TestDeltasCorrupt(t *testing.T) {
            rs := []Reading{{Value: 900, At: at(1)}, {Value: 100, At: at(2)}}
            if _, err := Deltas(rs, 500); err == nil {
                t.Fatal("want error")
            }
        }
        '''))

    # ---------------------------------------------------------------- ingest
    t.fx("ingest/csv.go", d('''
        // Package ingest parses the CSV exports of the head-end system.
        package ingest

        import (
            "encoding/csv"
            "fmt"
            "io"
            "strconv"
            "strings"
            "time"

            "meterd/readings"
        )

        // Meter is one installed meter.
        type Meter struct {
            ID, Customer, Model string
        }

        // Readings parses "meter,at,value" rows (header required).
        func Readings(r io.Reader) ([]readings.Reading, error) {
            rows, err := records(r, "meter,at,value")
            if err != nil {
                return nil, err
            }
            out := make([]readings.Reading, 0, len(rows))
            for i, row := range rows {
                at, err := time.Parse(time.RFC3339, row[1])
                if err != nil {
                    return nil, fmt.Errorf("readings line %d: %w", i+2, err)
                }
                v, err := strconv.ParseInt(row[2], 10, 64)
                if err != nil {
                    return nil, fmt.Errorf("readings line %d: %w", i+2, err)
                }
                out = append(out, readings.Reading{Meter: row[0], At: at, Value: v})
            }
            return out, nil
        }

        // Meters parses "meter,customer,model" rows (header required).
        func Meters(r io.Reader) ([]Meter, error) {
            rows, err := records(r, "meter,customer,model")
            if err != nil {
                return nil, err
            }
            out := make([]Meter, 0, len(rows))
            for _, row := range rows {
                out = append(out, Meter{ID: row[0], Customer: row[1], Model: row[2]})
            }
            return out, nil
        }

        func records(r io.Reader, header string) ([][]string, error) {
            cr := csv.NewReader(r)
            all, err := cr.ReadAll()
            if err != nil {
                return nil, err
            }
            if len(all) == 0 || strings.Join(all[0], ",") != header {
                return nil, fmt.Errorf("want header %q", header)
            }
            return all[1:], nil
        }
        '''))
    t.fx("ingest/csv_test.go", d('''
        package ingest

        import (
            "strings"
            "testing"
        )

        func TestParse(t *testing.T) {
            rs, err := Readings(strings.NewReader("meter,at,value\\nM-1,2027-03-01T06:00:00Z,42\\n"))
            if err != nil || len(rs) != 1 || rs[0].Value != 42 {
                t.Fatalf("%v %v", rs, err)
            }
            ms, err := Meters(strings.NewReader("meter,customer,model\\nM-1,C-1,EM-5\\n"))
            if err != nil || ms[0].Model != "EM-5" {
                t.Fatalf("%v %v", ms, err)
            }
            if _, err := Meters(strings.NewReader("id,model\\n")); err == nil {
                t.Fatal("want header error")
            }
        }
        '''))

    # ---------------------------------------------------------------- usage
    t.fx("usage/aggregate.go", d('''
        // Package usage turns raw readings into consumption per customer.
        package usage

        import (
            "fmt"
            "sort"

            "meterd/ingest"
            "meterd/meter"
            "meterd/readings"
        )

        // PerCustomer sums the consumption (kWh) of every customer's meters.
        func PerCustomer(meters []ingest.Meter, rs []readings.Reading) (map[string]int64, error) {
            byMeter := readings.ByMeter(rs)
            ids := make([]string, 0, len(meters))
            info := map[string]ingest.Meter{}
            for _, m := range meters {
                ids = append(ids, m.ID)
                info[m.ID] = m
            }
            sort.Strings(ids)
            out := map[string]int64{}
            for _, id := range ids {
                m := info[id]
                series := byMeter[id]
                readings.SortByTime(series)
                ds, err := readings.Deltas(series, meter.Modulus(m.Model))
                if err != nil {
                    return nil, fmt.Errorf("usage: meter %s: %w", id, err)
                }
                out[m.Customer] += readings.Sum(ds)
            }
            return out, nil
        }
        '''))
    t.fx("usage/aggregate_test.go", d('''
        package usage

        import (
            "testing"
            "time"

            "meterd/ingest"
            "meterd/readings"
        )

        func TestPerCustomer(t *testing.T) {
            d := func(day int) time.Time { return time.Date(2027, 2, day, 6, 0, 0, 0, time.UTC) }
            ms := []ingest.Meter{{ID: "a", Customer: "c1", Model: "EM-5"}, {ID: "b", Customer: "c1", Model: "EMX"}}
            rs := []readings.Reading{{Meter: "a", At: d(2), Value: 15}, {Meter: "a", At: d(1), Value: 10},
                {Meter: "b", At: d(1), Value: 1}, {Meter: "b", At: d(2), Value: 4}}
            got, err := PerCustomer(ms, rs)
            if err != nil || got["c1"] != 8 {
                t.Fatalf("%v %v", got, err)
            }
        }
        '''))

    # ---------------------------------------------------------------- tariff
    t.fx("tariff/tariff.go", d('''
        // Package tariff prices monthly consumption.
        package tariff

        // Tier charges Rate grosze per kWh for consumption up to UpTo kWh
        // (UpTo 0 = no upper bound; must be last).
        type Tier struct {
            UpTo int64
            Rate int64
        }

        // Tariff is a tiered price list plus a fixed monthly fee in grosze.
        type Tariff struct {
            Tiers    []Tier
            FixedFee int64
        }

        // Household is the regulated household tariff G11.
        var Household = Tariff{
            Tiers:    []Tier{{UpTo: 300, Rate: 62}, {UpTo: 1000, Rate: 71}, {Rate: 85}},
            FixedFee: 1850,
        }

        // Price returns the monthly amount in grosze for kwh.
        func (t Tariff) Price(kwh int64) int64 {
            amount := t.FixedFee
            var prev int64
            for _, tier := range t.Tiers {
                top := kwh
                if tier.UpTo != 0 && tier.UpTo < kwh {
                    top = tier.UpTo
                }
                if top > prev {
                    amount += (top - prev) * tier.Rate
                    prev = top
                }
            }
            return amount
        }
        '''))
    t.fx("tariff/tariff_test.go", d('''
        package tariff

        import "testing"

        func TestPrice(t *testing.T) {
            cases := map[int64]int64{0: 1850, 100: 1850 + 6200, 300: 1850 + 18600, 400: 1850 + 18600 + 7100,
                1200: 1850 + 18600 + 49700 + 17000}
            for kwh, want := range cases {
                if got := Household.Price(kwh); got != want {
                    t.Errorf("Price(%d) = %d, want %d", kwh, got, want)
                }
            }
        }
        '''))

    # ---------------------------------------------------------------- billing (where the test fails)
    t.fx("billing/invoice.go", d('''
        // Package billing builds monthly invoices.
        package billing

        import (
            "fmt"
            "io"
            "sort"

            "meterd/ingest"
            "meterd/tariff"
            "meterd/usage"
        )

        // Invoice is one customer's monthly bill.
        type Invoice struct {
            Customer string
            KWh      int64
            Amount   int64 // grosze
        }

        // Build reads the meter list and readings exports and prices every
        // customer's consumption with t. Invoices are sorted by customer.
        func Build(metersCSV, readingsCSV io.Reader, t tariff.Tariff) ([]Invoice, error) {
            ms, err := ingest.Meters(metersCSV)
            if err != nil {
                return nil, fmt.Errorf("billing: meters: %w", err)
            }
            rs, err := ingest.Readings(readingsCSV)
            if err != nil {
                return nil, fmt.Errorf("billing: readings: %w", err)
            }
            perCustomer, err := usage.PerCustomer(ms, rs)
            if err != nil {
                return nil, fmt.Errorf("billing: %w", err)
            }
            out := make([]Invoice, 0, len(perCustomer))
            for c, kwh := range perCustomer {
                // TODO(finance): amounts are whole grosze; revisit rounding if we
                // ever bill fractional kWh.
                out = append(out, Invoice{Customer: c, KWh: kwh, Amount: t.Price(kwh)})
            }
            sort.Slice(out, func(i, j int) bool { return out[i].Customer < out[j].Customer })
            return out, nil
        }

        // Total sums the amounts of invs.
        func Total(invs []Invoice) (kwh, amount int64) {
            for _, inv := range invs {
                kwh += inv.KWh
                amount += inv.Amount
            }
            return kwh, amount
        }
        '''))
    t.fx("billing/format.go", d('''
        package billing

        import (
            "fmt"
            "strings"
        )

        // FormatPLN renders grosze as "1 234,56 zł".
        func FormatPLN(grosze int64) string {
            neg := grosze < 0
            if neg {
                grosze = -grosze
            }
            whole := fmt.Sprintf("%d", grosze/100)
            var b strings.Builder
            for i, ch := range whole {
                if i > 0 && (len(whole)-i)%3 == 0 {
                    b.WriteByte(' ')
                }
                b.WriteRune(ch)
            }
            s := fmt.Sprintf("%s,%02d zł", b.String(), grosze%100)
            if neg {
                s = "-" + s
            }
            return s
        }
        '''))
    lines = "\n".join('        "%s": {%d, %d},' % (c, usage[c], bills[c]) for c in (plain_cust, roll5_cust, roll6_cust))
    total_kwh, total_amount = sum(usage.values()), sum(bills.values())
    t.fx("billing/invoice_test.go", d('''
        package billing

        import (
            "os"
            "path/filepath"
            "testing"

            "meterd/tariff"
        )

        func open(t *testing.T, name string) *os.File {
            t.Helper()
            f, err := os.Open(filepath.Join("..", "testdata", "march", name))
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { f.Close() })
            return f
        }

        func TestMarchInvoices(t *testing.T) {
            invs, err := Build(open(t, "meters.csv"), open(t, "readings.csv"), tariff.Household)
            if err != nil {
                t.Fatal(err)
            }
            if len(invs) != %(n)d {
                t.Fatalf("got %%d invoices, want %(n)d", len(invs))
            }
            want := map[string][2]int64{
        %(lines)s
            }
            for _, inv := range invs {
                if w, ok := want[inv.Customer]; ok && (inv.KWh != w[0] || inv.Amount != w[1]) {
                    t.Errorf("%%s: got %%d kWh / %%s, want %%d kWh / %%s", inv.Customer, inv.KWh, FormatPLN(inv.Amount), w[0], FormatPLN(w[1]))
                }
            }
            kwh, amount := Total(invs)
            if kwh != %(tk)d || amount != %(ta)d {
                t.Fatalf("total: got %%d kWh / %%s, want %(tk)d kWh / %%s", kwh, FormatPLN(amount), FormatPLN(%(ta)d))
            }
        }

        func TestFormatPLN(t *testing.T) {
            for in, want := range map[int64]string{0: "0,00 zł", 123456: "1 234,56 zł", -5: "-0,05 zł"} {
                if got := FormatPLN(in); got != want {
                    t.Errorf("FormatPLN(%%d) = %%q, want %%q", in, got, want)
                }
            }
        }
        ''') % {"n": len(usage), "lines": lines, "tk": total_kwh, "ta": total_amount})
    t.fx("cmd/billrun/main.go", d('''
        // Command billrun prints the invoices for one month of exports.
        package main

        import (
            "fmt"
            "os"

            "meterd/billing"
            "meterd/tariff"
        )

        func main() {
            if len(os.Args) != 3 {
                fmt.Fprintln(os.Stderr, "usage: billrun meters.csv readings.csv")
                os.Exit(2)
            }
            m, err := os.Open(os.Args[1])
            if err != nil {
                fmt.Fprintln(os.Stderr, err)
                os.Exit(1)
            }
            defer m.Close()
            r, err := os.Open(os.Args[2])
            if err != nil {
                fmt.Fprintln(os.Stderr, err)
                os.Exit(1)
            }
            defer r.Close()
            invs, err := billing.Build(m, r, tariff.Household)
            if err != nil {
                fmt.Fprintln(os.Stderr, err)
                os.Exit(1)
            }
            for _, inv := range invs {
                fmt.Printf("%s\\t%d kWh\\t%s\\n", inv.Customer, inv.KWh, billing.FormatPLN(inv.Amount))
            }
        }
        '''))
    t.fx("README.md", d('''
        # meterd

        Monthly electricity billing from head-end exports. Packages: ingest (CSV),
        meter (model registry), readings (consumption), usage (per customer),
        tariff (prices), billing (invoices). `go test ./...` runs everything against
        the March 2027 exports in testdata/march.
        '''))
    for p in ["outage", "crm", "audit", "notify"]:
        go_filler(t, p, count=4)

    t.ref("meter/normalize.go", normalize.replace("if unicode.IsLetter(r) {", "if unicode.IsLetter(r) || unicode.IsDigit(r) {"))
    t.answer("Root cause: meter.normalizeModel kept only letters, so \"EM-6\" normalised to \"EM\", which is not in the "
             "registry; Digits fell back to 5 and the 6-digit meter's wrap-around was computed with modulus 100000, "
             "so readings.Deltas reported a negative delta and billing failed. normalizeModel now keeps digits too. "
             "go test ./... passes.\n")
    t.gr("meter/zz_hidden_test.go", d('''
        package meter

        import "testing"

        func TestHiddenModelNames(t *testing.T) {
            cases := map[string]int{
                "EM-6": 6, "em6": 6, " Em 6 ": 6, "EM.6": 6, "em-5": 5, "EM5": 5,
                "EMX": 7, "em_x": 7, "E M X": 7, "XR-200": 5, "EM-56": 5, "": 5,
            }
            for model, want := range cases {
                if got := Digits(model); got != want {
                    t.Errorf("Digits(%q) = %d, want %d", model, got, want)
                }
            }
            if Modulus("em-6") != 1000000 || Modulus("EMX") != 10000000 || Modulus("nope") != 100000 {
                t.Fatal("Modulus")
            }
            if !Known("Em 6") || Known("EM-56") {
                t.Fatal("Known")
            }
        }
        '''))
    t.register(
        name="Debug a billing failure whose error is raised two packages away from the real cause (many packages, 1k-row export)",
        category="long-context", tags=["go", "debugging", "long", "root-cause", "misleading-symptom"], difficulty="hard",
        prompt=("`go test ./...` fails in the billing package of this Go module. Find the root cause and fix it "
                "where it actually is. Do not change the existing tests or anything under testdata/, and do not "
                "special-case particular meters, customers or values. When you are done, explain the root cause in "
                "two or three sentences. `go vet ./...` and `go test ./...` must pass."),
        strictDiff=True,
        allowedChangedFiles=["meter/*.go"],
        forbiddenChangedFiles=["meter/registry_test.go", "testdata/**", "readings/**", "usage/**", "billing/**",
                               "ingest/**", "tariff/**", "cmd/**", "outage/**", "crm/**", "audit/**", "notify/**", "go.mod"],
        expectedChangedFiles=["meter/normalize.go"],
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./...")],
        fileChecks=[
            {"id": "registry-intact", "path": "meter/registry.go",
             "regex": [r'"EM5":\s*5', r'"EM6":\s*6', r'"EMX":\s*7', r"DefaultDigits\s*=\s*5"], "critical": True},
        ],
        answerChecks=[{"id": "explains-cause", "anyOf": [r"normali[sz]", r"digit", r"IsLetter", r"model name"],
                       "regex": True}],
    )
