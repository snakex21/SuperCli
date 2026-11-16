"""Data transformation with an exact expected output: Polish-format CSV in,
JSON summary + monthly CSV out, following a written spec with traps."""
import csv
import io
import json
from decimal import ROUND_HALF_UP, Decimal

from common import GRADER_PRELUDE, Task, cmd, d, py_literal

SPEC = d('''
    # Specyfikacja / Specification: transaction summary

    Input: `input/transakcje.csv` and `input/kursy.json`. Output: `output/summary.json` and `output/miesiecznie.csv`.

    ## Input

    `input/transakcje.csv` - UTF-8, separator `;`, first line is the header
    `id;data;klient;kwota;waluta;status;opis`. Fields may be quoted with `"` (a quoted field may contain `;`,
    and `""` inside quotes is a literal quote).

    - `id` - transaction id. **The same id can appear more than once; only its first occurrence in the file
      counts** (regardless of status); later rows with that id are duplicates.
    - `data` - date `DD.MM.YYYY`.
    - `klient` - customer name; trim surrounding whitespace, otherwise keep it exactly (case, Polish letters, commas).
    - `kwota` - amount in Polish format: optional leading `-`, thousands separated by a space or a no-break space
      (U+00A0), decimal comma, always two decimals (`1 234,50`, `-50,00`). Negative amounts are refunds.
    - `waluta` - `PLN` or `EUR`. EUR amounts are converted to PLN with the rate for the transaction's month from
      `input/kursy.json` (`"YYYY-MM": "rate"`): `PLN grosze = round_half_up(EUR cents x rate)` to a whole grosz.
    - `status` - only `ZAKSIĘGOWANA` rows are counted. `ANULOWANA` and `OCZEKUJĄCA` rows are skipped.

    A row is *counted* when it is the first occurrence of its id and its status is `ZAKSIĘGOWANA`.

    ## output/summary.json

    ```json
    {
      "customers": [
        {"klient": "<name>", "transakcje": <counted rows>, "suma_gr": <sum of counted amounts in grosze, refunds included>,
         "zwroty_gr": <sum of the absolute values of counted negative amounts, in grosze>}
      ],
      "razem_gr": <sum of all counted amounts in grosze>,
      "pominiete": {"anulowane": <n>, "oczekujace": <n>, "duplikaty": <n>}
    }
    ```

    - `customers`: one entry per customer with at least one counted row, sorted by `suma_gr` descending, then by
      `klient` ascending (plain Unicode code point order).
    - `pominiete` counts skipped rows: duplicates (any status) are counted only as `duplikaty`; first occurrences
      are counted as `anulowane` / `oczekujace` by status.
    - All amounts are integers (grosze). Key order and whitespace in the JSON file do not matter.

    ## output/miesiecznie.csv

    Comma-separated, header `miesiac,klient,suma_pln`, one row per (month, customer) with at least one counted row:

    - `miesiac` - `YYYY-MM` of the transaction date;
    - `klient` - as above (quote it per RFC 4180 when it contains a comma or a quote);
    - `suma_pln` - sum of the counted amounts in PLN with a dot and exactly two decimals (`1234.50`, `-50.00`), no
      thousands separator.

    Rows sorted by `miesiac`, then `klient` (Unicode code point order).
    ''')

RATES = {"2026-01": "4.2650", "2026-02": "4.2380", "2026-03": "4.2715"}

CUSTOMERS = ["Hurtownia Łąka sp. z o.o.", "Kowalski, Jan", "Źródło S.A.", "Apteka \"Pod Orłem\"", "ZIELONY GAJ",
             "Zielony Gaj", "Ćma i Syn", "Bąk Tomasz"]


def pl_amount(grosze, rng):
    neg = grosze < 0
    g = abs(grosze)
    zl, gr = divmod(g, 100)
    s = str(zl)
    if zl >= 1000:
        sep = rng.choice([" ", " "])
        parts = []
        while s:
            parts.insert(0, s[-3:])
            s = s[:-3]
        s = sep.join(parts)
    return ("-" if neg else "") + s + "," + "%02d" % gr


def build():
    t = Task("data-ledger-transform")
    rng = t.rng
    rows = []
    n = 0
    ids = []
    for month in (1, 2, 3):
        for _ in range(rng.randint(16, 20)):
            n += 1
            tid = "T-%04d" % n
            day = rng.randint(1, 28)
            cust = rng.choice(CUSTOMERS)
            cur = "EUR" if rng.random() < 0.2 else "PLN"
            if rng.random() < 0.15:
                gr = -rng.choice([5000, 1999, 125050, 30000])
            else:
                gr = rng.choice([rng.randint(100, 99999), rng.randint(100000, 2500000)])
            status = rng.choices(["ZAKSIĘGOWANA", "ANULOWANA", "OCZEKUJĄCA"], [8, 1, 1])[0]
            opis = rng.choice(["Faktura %s" % tid, "Zwrot; reklamacja", "Usługa \"premium\"", "", "Dostawa nr %d" % n])
            rows.append([tid, "%02d.%02d.2026" % (day, month), cust, gr, cur, status, opis])
            ids.append(tid)
    # duplicates: re-emitted rows (sometimes with a different status) later in the file
    for k in range(6):
        src = rng.choice(rows[:40])
        dup = list(src)
        dup[5] = rng.choice(["ZAKSIĘGOWANA", "ANULOWANA"])
        rows.insert(rng.randint(rows.index(src) + 1, len(rows)), dup)
    # whitespace around some names
    out = io.StringIO()
    w = csv.writer(out, delimiter=";", lineterminator="\n", quoting=csv.QUOTE_MINIMAL)
    w.writerow(["id", "data", "klient", "kwota", "waluta", "status", "opis"])
    for r in rows:
        name = r[2]
        if rng.random() < 0.2:
            name = rng.choice(["  ", " "]) + name + rng.choice(["", " ", "  "])
        w.writerow([r[0], r[1], name, pl_amount(r[3], rng), r[4], r[5], r[6]])
    csv_text = out.getvalue()

    # ---- expected output (independent of the writer above: parse the text back)
    seen = set()
    counted = []
    skipped = {"anulowane": 0, "oczekujace": 0, "duplikaty": 0}
    for rec in list(csv.reader(io.StringIO(csv_text), delimiter=";"))[1:]:
        tid, date, cust, amount, cur, status, _ = rec
        if tid in seen:
            skipped["duplikaty"] += 1
            continue
        seen.add(tid)
        if status == "ANULOWANA":
            skipped["anulowane"] += 1
            continue
        if status == "OCZEKUJĄCA":
            skipped["oczekujace"] += 1
            continue
        assert status == "ZAKSIĘGOWANA"
        value = Decimal(amount.replace(" ", "").replace(" ", "").replace(",", "."))
        cents = int(value * 100)
        dd, mm, yyyy = date.split(".")
        month = "%s-%s" % (yyyy, mm)
        if cur == "EUR":
            frac = abs(Decimal(cents) * Decimal(RATES[month])) % 1
            assert frac != Decimal("0.5"), (tid, cents)  # keep round-half-up unambiguous for negatives
            cents = int((Decimal(cents) * Decimal(RATES[month])).quantize(Decimal("1"), rounding=ROUND_HALF_UP))
        counted.append((month, cust.strip(), cents))
    per = {}
    for month, cust, cents in counted:
        e = per.setdefault(cust, {"klient": cust, "transakcje": 0, "suma_gr": 0, "zwroty_gr": 0})
        e["transakcje"] += 1
        e["suma_gr"] += cents
        if cents < 0:
            e["zwroty_gr"] += -cents
    customers = sorted(per.values(), key=lambda e: (-e["suma_gr"], e["klient"]))
    summary = {"customers": customers, "razem_gr": sum(c for _, _, c in counted), "pominiete": skipped}
    monthly = {}
    for month, cust, cents in counted:
        monthly[(month, cust)] = monthly.get((month, cust), 0) + cents

    def fmt(c):
        return ("-" if c < 0 else "") + "%d.%02d" % divmod(abs(c), 100)

    m_rows = [["miesiac", "klient", "suma_pln"]] + [[m, c, fmt(v)] for (m, c), v in sorted(monthly.items())]
    mo = io.StringIO()
    csv.writer(mo, lineterminator="\n").writerows(m_rows)
    assert skipped["duplikaty"] == 6 and skipped["anulowane"] > 0 and skipped["oczekujace"] > 0, skipped
    assert any(c < 0 for _, _, c in counted) and any("," in c for c in per) and len(per) == len(CUSTOMERS), per.keys()

    t.fx("SPEC.md", SPEC)
    t.fx("input/transakcje.csv", csv_text)
    t.fx("input/kursy.json", json.dumps(RATES, indent=2) + "\n")
    t.fx("README.md", "# ledger\n\nMonthly transaction summary for the accounting team. See SPEC.md.\n")

    argv = t.grade_script(GRADER_PRELUDE + "SUMMARY = " + py_literal(summary) + "\nMONTHLY = " + py_literal(m_rows) + "\n" + d(r'''
        import csv
        import io
        import json

        try:
            with open("output/summary.json", encoding="utf-8-sig") as f:
                got = json.load(f)
            ok = True
        except Exception as e:  # noqa: BLE001
            check("summary.json is valid JSON", False, e)
            got, ok = {}, False
        if ok:
            check("razem_gr", got.get("razem_gr") == SUMMARY["razem_gr"], (got.get("razem_gr"), SUMMARY["razem_gr"]))
            check("pominiete", got.get("pominiete") == SUMMARY["pominiete"], (got.get("pominiete"), SUMMARY["pominiete"]))
            gc = got.get("customers")
            check("customers (values and order)", gc == SUMMARY["customers"],
                  next(("#%d got %s want %s" % (i, a, b) for i, (a, b) in enumerate(zip(gc or [], SUMMARY["customers"])) if a != b),
                       "length %s vs %d" % (len(gc) if isinstance(gc, list) else gc, len(SUMMARY["customers"]))))
            check("no extra top-level keys", set(got) == set(SUMMARY), sorted(set(got) ^ set(SUMMARY)))
        try:
            with open("output/miesiecznie.csv", encoding="utf-8-sig", newline="") as f:
                rows = [r for r in csv.reader(f) if r]
            check("miesiecznie.csv header", rows[:1] == MONTHLY[:1], rows[:1])
            bad = [(i, a, b) for i, (a, b) in enumerate(zip(rows, MONTHLY)) if a != b]
            check("miesiecznie.csv rows", not bad and len(rows) == len(MONTHLY),
                  bad[:2] if bad else "rows %d want %d" % (len(rows), len(MONTHLY)))
        except Exception as e:  # noqa: BLE001
            check("miesiecznie.csv readable", False, e)
        finish()
        '''))
    out_json = json.dumps(summary, ensure_ascii=False, indent=2) + "\n"
    t.solve_files({"output/summary.json": out_json, "output/miesiecznie.csv": mo.getvalue()}, answer=(
        "Generated output/summary.json (%d customers, razem_gr=%d; skipped: %s) and output/miesiecznie.csv (%d rows) "
        "following SPEC.md: first occurrence of each id only, ZAKSIĘGOWANA rows only, Polish amounts parsed (space / "
        "no-break space thousands, decimal comma), EUR converted per month with round-half-up to grosze."
        % (len(customers), summary["razem_gr"], skipped, len(m_rows) - 1)))
    t.register(
        name="Exact CSV-to-JSON/CSV transformation of Polish-format ledger data per a written spec",
        description=("Parse a semicolon CSV with quoted fields, Polish number format (space / NBSP thousands, "
                     "decimal comma), duplicates, statuses and EUR conversion with per-month rates; produce a JSON "
                     "summary and a monthly CSV. Graded by exact comparison of the parsed outputs."),
        category="data",
        tags=["data", "csv", "json", "exact-output", "polish-format"],
        difficulty="medium",
        prompt=("Implement the transformation described in `SPEC.md` and produce both output files "
                "(`output/summary.json` and `output/miesiecznie.csv`) from the files in `input/`. You may write a script "
                "(put it under `tools/`), but what I need are the two output files with exactly the content the spec defines. "
                "Don't modify the input files or the spec."),
        expectedChangedFiles=["output/summary.json", "output/miesiecznie.csv"],
        forbiddenChangedFiles=["input/**", "SPEC.md"],
        strictDiff=True,
        allowedChangedFiles=["output/**", "tools/**"],
        verificationCommands=[cmd("exact-outputs", *argv)],
    )
