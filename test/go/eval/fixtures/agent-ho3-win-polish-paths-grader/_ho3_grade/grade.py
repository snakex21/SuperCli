import os
import sys

sys.dont_write_bytecode = True
try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass

_results = []


def check(name, ok, detail=""):
    _results.append((name, bool(ok)))
    print(("PASS " if ok else "FAIL ") + name + (("  -- " + str(detail)) if (detail and not ok) else ""))
    return bool(ok)


def finish():
    passed = sum(1 for _, ok in _results if ok)
    total = len(_results) or 1
    if passed == total:
        print("all %d checks passed" % total)
        sys.exit(0)
    print("EVAL_SCORE=%.3f" % (passed / total))
    sys.exit(1)


def lp(path):
    """Long-path safe absolute path on Windows."""
    p = os.path.abspath(path)
    if os.name == "nt" and not p.startswith("\\\\?\\"):
        p = "\\\\?\\" + p
    return p


def read_bytes(path):
    with open(lp(path), "rb") as f:
        return f.read()


def norm_text(b):
    if isinstance(b, bytes):
        b = b.decode("utf-8-sig", "replace")
    lines = b.replace("\r\n", "\n").split("\n")
    return "\n".join(l.rstrip(" \t\r") for l in lines).rstrip("\n")
DATA = {
 "expected_md": [
  "README.md",
  "docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md",
  "docs/Faktury/FV 2024-03 \u2013 Krak\u00f3w.md",
  "docs/Polityka prywatno\u015bci.md",
  "docs/Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md",
  "docs/index.md"
 ],
 "original": {
  "README.md": "# Dokumentacja sp\u00f3\u0142ki\n\nNajwa\u017cniejsze dokumenty:\n\n- [Sprawozdanie roczne](<docs/Raporty roczne/za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144.md>)\n- [Podsumowanie 2023](docs/Raporty%20roczne/2023%20%E2%80%93%20podsumowanie.md)\n- [Polityka prywatno\u015bci](<docs/Polityka prywatno\u015bci.md>)\n\nStrona dokumentacji budowana jest przez `mkdocs build` (konfiguracja w `mkdocs.yml`).\n",
  "docs/Faktury/FV 2024-03 \u2013 Krak\u00f3w.md": "# FV 2024-03 \u2013 Krak\u00f3w\n\nFaktura za us\u0142ugi doradcze. Zobacz [sprawozdanie](<../Raporty roczne/za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144.md>).\n",
  "docs/Polityka prywatno\u015bci.md": "# Polityka prywatno\u015bci\n\nAdministratorem danych jest Sp\u00f3\u0142ka.\n",
  "docs/Raporty roczne/2023 \u2013 podsumowanie.md": "# Podsumowanie 2023\n\nRok zamkni\u0119ty zyskiem netto 2,4 mln z\u0142.\n\nNast\u0119pny raport: [sprawozdanie 2024](za%C5%BC%C3%B3%C5%82%C4%87%20g%C4%99%C5%9Bl%C4%85%20ja%C5%BA%C5%84.md). Powr\u00f3t: [spis tre\u015bci](../index.md).\n",
  "docs/Raporty roczne/za\u0142\u0105czniki/wykres sprzeda\u017cy.csv": "miesi\u0105c;sprzeda\u017c\r\nstycze\u0144;120\r\nluty;132\r\n",
  "docs/Raporty roczne/za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144.md": "# Sprawozdanie roczne 2024\n\nPrzychody wzros\u0142y o 12% r/r. Szczeg\u00f3\u0142y w [za\u0142\u0105czniku](<za\u0142\u0105czniki/wykres sprzeda\u017cy.csv>).\n\nPor\u00f3wnaj z [poprzednim rokiem](<2023 \u2013 podsumowanie.md>) oraz wr\u00f3\u0107 do [spisu tre\u015bci](../index.md).\n\n## Struktura przychod\u00f3w\n\n| Segment | Udzia\u0142 |\n|---|---|\n| Hurt | 61% |\n| Detal | 39% |\n",
  "docs/index.md": "# Start\n\n- [Raport roczny](Raporty%20roczne/za%C5%BC%C3%B3%C5%82%C4%87%20g%C4%99%C5%9Bl%C4%85%20ja%C5%BA%C5%84.md)\n- [Podsumowanie 2023](<Raporty roczne/2023 \u2013 podsumowanie.md>)\n- [Faktura marcowa](Faktury/FV%202024-03%20%E2%80%93%20Krak%C3%B3w.md)\n- [Polityka prywatno\u015bci](<Polityka prywatno\u015bci.md>)\n",
  "mkdocs.yml": "site_name: Dokumentacja sp\u00f3\u0142ki\ndocs_dir: docs\nnav:\n  - Start: index.md\n  - Raporty:\n      - \"Sprawozdanie roczne\": \"Raporty roczne/za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144.md\"\n      - \"Podsumowanie 2023\": \"Raporty roczne/2023 \u2013 podsumowanie.md\"\n  - Faktury: \"Faktury/FV 2024-03 \u2013 Krak\u00f3w.md\"\n  - \"Polityka prywatno\u015bci\": \"Polityka prywatno\u015bci.md\"\n"
 },
 "renamed": {
  "docs/Raporty roczne/2023 \u2013 podsumowanie.md": "docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md",
  "docs/Raporty roczne/za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144.md": "docs/Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md"
 },
 "style": [
  [
   "docs/index.md",
   "Raport roczny",
   "pct",
   "Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md"
  ],
  [
   "docs/index.md",
   "Podsumowanie 2023",
   "angle",
   "Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md"
  ],
  [
   "README.md",
   "Sprawozdanie roczne",
   "angle",
   "docs/Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md"
  ],
  [
   "README.md",
   "Podsumowanie 2023",
   "pct",
   "docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md"
  ],
  [
   "docs/Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md",
   "poprzednim rokiem",
   "angle",
   "../Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md"
  ],
  [
   "docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md",
   "sprawozdanie 2024",
   "pct",
   "../../Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md"
  ],
  [
   "docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md",
   "spis tre\u015bci",
   "plain",
   "../../index.md"
  ]
 ],
 "unchanged": [
  "docs/Polityka prywatno\u015bci.md",
  "docs/Raporty roczne/za\u0142\u0105czniki/wykres sprzeda\u017cy.csv"
 ]
}
import re
import unicodedata
import urllib.parse

NFC = lambda s: unicodedata.normalize("NFC", s)


def actual_files():
    out = {}
    for root, dirs, files in os.walk("."):
        dirs[:] = [x for x in dirs if x not in (".git", "_ho3_grade", "__pycache__", ".supercli")]
        for n in files:
            rel = os.path.relpath(os.path.join(root, n), ".").replace(os.sep, "/")
            out[NFC(rel)] = rel
    return out


FILES = actual_files()
exists = lambda rel: NFC(rel) in FILES
real = lambda rel: FILES[NFC(rel)]

for old, new in sorted(DATA["renamed"].items()):
    check("old path gone: " + old, not exists(old))
    check("new path exists: " + new, exists(new))
md_now = sorted(k for k in FILES if k.endswith(".md"))
check("exactly the expected Markdown files", md_now == sorted(NFC(x) for x in DATA["expected_md"]),
      sorted(set(md_now) ^ set(NFC(x) for x in DATA["expected_md"])))

LINK = re.compile(r"\[([^\]]*)\]\((<[^>]*>|[^)\s]*)\)")


def strip_links(text):
    return LINK.sub(lambda m: "[" + m.group(1) + "]()", norm_text(text))


def decode(target):
    if target.startswith("<") and target.endswith(">"):
        return target[1:-1], "angle"
    dec = urllib.parse.unquote(target)
    return dec, ("pct" if dec != target else "plain")


# Moved/renamed and other edited files keep their text apart from link targets.
for old, new in sorted(DATA["renamed"].items()):
    if exists(new):
        check("content kept: " + new, strip_links(read_bytes(real(new))) == strip_links(DATA["original"][old]))
for rel in ("README.md", "docs/index.md", "docs/Faktury/FV 2024-03 – Kraków.md"):
    if exists(rel):
        check("text kept apart from links: " + rel, strip_links(read_bytes(real(rel))) == strip_links(DATA["original"][rel]))
for rel in DATA["unchanged"]:
    check("unchanged: " + rel, exists(rel) and read_bytes(real(rel)) == DATA["original"][rel].encode("utf-8"))

# Every relative link in every Markdown file resolves.
broken = []
for rel in md_now:
    text = read_bytes(real(rel)).decode("utf-8", "replace")
    base = os.path.dirname(rel)
    for m in LINK.finditer(text):
        target, _ = decode(m.group(2))
        target = target.split("#", 1)[0]
        if not target or re.match(r"^[a-z]+:", target):
            continue
        full = NFC(os.path.normpath(os.path.join(base, target)).replace(os.sep, "/"))
        if full not in FILES:
            broken.append(rel + " -> " + m.group(2))
check("all Markdown links resolve", not broken, broken[:6])

# Link style preserved for the links that had to change.
for rel, label, style, want in DATA["style"]:
    if not exists(rel):
        check("style %s in %s" % (label, rel), False, "file missing")
        continue
    text = read_bytes(real(rel)).decode("utf-8", "replace")
    hits = [m.group(2) for m in LINK.finditer(text) if m.group(1) == label]
    ok = False
    got = hits
    for h in hits:
        dec, st = decode(h)
        if NFC(dec) == NFC(want) and (st == style or (style == "plain" and st == "pct")):
            ok = True
    check("link '%s' in %s is %s and points to the new path" % (label, rel, style), ok, got)

# mkdocs nav (paths relative to docs/).
yml = read_bytes("mkdocs.yml").decode("utf-8")
navs = re.findall(r'"([^"]+\.md)"|:[ \t]+([^\s"][^\n]*\.md)[ \t]*$', yml, re.M)
missing = [p for pair in navs for p in pair if p and NFC("docs/" + p) not in FILES]
check("mkdocs nav paths resolve", not missing, missing)
check("mkdocs.yml otherwise unchanged",
      re.sub(r'"[^"]*\.md"', '""', norm_text(yml)) == re.sub(r'"[^"]*\.md"', '""', norm_text(DATA["original"]["mkdocs.yml"])))
finish()
