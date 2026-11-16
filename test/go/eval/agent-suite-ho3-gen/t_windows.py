"""Windows specifics: Polish/space paths with link fixing, CRLF + BOM byte
preservation, and an archive whose target paths exceed MAX_PATH."""
import json
import urllib.parse

from common import BOM, GRADER_PRELUDE, Task, cmd, crlf, d, py_literal, sub

OLD_REPORT = "docs/Raporty roczne/zażółć gęślą jaźń.md"
NEW_REPORT = "docs/Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md"
OLD_SUMMARY = "docs/Raporty roczne/2023 – podsumowanie.md"
NEW_SUMMARY = "docs/Archiwum/Lata ubiegłe/2023 – podsumowanie.md"


def pct(path):
    return urllib.parse.quote(path, safe="/")


def build_polish_paths():
    t = Task("win-polish-paths")
    files = {}
    files["README.md"] = d('''
        # Dokumentacja spółki

        Najważniejsze dokumenty:

        - [Sprawozdanie roczne](<docs/Raporty roczne/zażółć gęślą jaźń.md>)
        - [Podsumowanie 2023](%s)
        - [Polityka prywatności](<docs/Polityka prywatności.md>)

        Strona dokumentacji budowana jest przez `mkdocs build` (konfiguracja w `mkdocs.yml`).
        ''') % pct("docs/Raporty roczne/2023 – podsumowanie.md")
    files["mkdocs.yml"] = d('''
        site_name: Dokumentacja spółki
        docs_dir: docs
        nav:
          - Start: index.md
          - Raporty:
              - "Sprawozdanie roczne": "Raporty roczne/zażółć gęślą jaźń.md"
              - "Podsumowanie 2023": "Raporty roczne/2023 – podsumowanie.md"
          - Faktury: "Faktury/FV 2024-03 – Kraków.md"
          - "Polityka prywatności": "Polityka prywatności.md"
        ''')
    files["docs/index.md"] = d('''
        # Start

        - [Raport roczny](%s)
        - [Podsumowanie 2023](<Raporty roczne/2023 – podsumowanie.md>)
        - [Faktura marcowa](%s)
        - [Polityka prywatności](<Polityka prywatności.md>)
        ''') % (pct("Raporty roczne/zażółć gęślą jaźń.md"), pct("Faktury/FV 2024-03 – Kraków.md"))
    files[OLD_REPORT] = d('''
        # Sprawozdanie roczne 2024

        Przychody wzrosły o 12%% r/r. Szczegóły w [załączniku](<załączniki/wykres sprzedaży.csv>).

        Porównaj z [poprzednim rokiem](<2023 – podsumowanie.md>) oraz wróć do [spisu treści](../index.md).

        ## Struktura przychodów

        | Segment | Udział |
        |---|---|
        | Hurt | 61%% |
        | Detal | 39%% |
        ''').replace("%%", "%")
    files[OLD_SUMMARY] = d('''
        # Podsumowanie 2023

        Rok zamknięty zyskiem netto 2,4 mln zł.

        Następny raport: [sprawozdanie 2024](%s). Powrót: [spis treści](../index.md).
        ''') % pct("zażółć gęślą jaźń.md")
    files["docs/Raporty roczne/załączniki/wykres sprzedaży.csv"] = "miesiąc;sprzedaż\r\nstyczeń;120\r\nluty;132\r\n"
    files["docs/Faktury/FV 2024-03 – Kraków.md"] = d('''
        # FV 2024-03 – Kraków

        Faktura za usługi doradcze. Zobacz [sprawozdanie](<../Raporty roczne/zażółć gęślą jaźń.md>).
        ''')
    files["docs/Polityka prywatności.md"] = "# Polityka prywatności\n\nAdministratorem danych jest Spółka.\n"
    for rel, c in files.items():
        t.fx(rel, c)

    # Expected final state (reference): links rewritten, style preserved.
    new = dict(files)
    del new[OLD_REPORT], new[OLD_SUMMARY]
    new["README.md"] = sub(files["README.md"],
                           ("<docs/Raporty roczne/zażółć gęślą jaźń.md>", "<%s>" % NEW_REPORT),
                           (pct("docs/Raporty roczne/2023 – podsumowanie.md"), pct(NEW_SUMMARY)))
    new["mkdocs.yml"] = sub(files["mkdocs.yml"],
                            ('"Raporty roczne/zażółć gęślą jaźń.md"', '"Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md"'),
                            ('"Raporty roczne/2023 – podsumowanie.md"', '"Archiwum/Lata ubiegłe/2023 – podsumowanie.md"'))
    new["docs/index.md"] = sub(files["docs/index.md"],
                               (pct("Raporty roczne/zażółć gęślą jaźń.md"), pct("Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md")),
                               ("<Raporty roczne/2023 – podsumowanie.md>", "<Archiwum/Lata ubiegłe/2023 – podsumowanie.md>"))
    new[NEW_REPORT] = sub(files[OLD_REPORT],
                          ("<2023 – podsumowanie.md>", "<../Archiwum/Lata ubiegłe/2023 – podsumowanie.md>"))
    new[NEW_SUMMARY] = sub(files[OLD_SUMMARY],
                           (pct("zażółć gęślą jaźń.md"), pct("../../Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md")),
                           ("(../index.md)", "(../../index.md)"))
    new["docs/Faktury/FV 2024-03 – Kraków.md"] = sub(files["docs/Faktury/FV 2024-03 – Kraków.md"],
                                                     ("<../Raporty roczne/zażółć gęślą jaźń.md>",
                                                      "<../Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md>"))

    data = {
        "original": files,
        "unchanged": ["docs/Polityka prywatności.md", "docs/Raporty roczne/załączniki/wykres sprzedaży.csv"],
        "renamed": {OLD_REPORT: NEW_REPORT, OLD_SUMMARY: NEW_SUMMARY},
        "expected_md": sorted(k for k in new if k.endswith(".md")),
        "style": [
            # file, link label, required style, expected decoded target
            ["docs/index.md", "Raport roczny", "pct", "Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md"],
            ["docs/index.md", "Podsumowanie 2023", "angle", "Archiwum/Lata ubiegłe/2023 – podsumowanie.md"],
            ["README.md", "Sprawozdanie roczne", "angle", NEW_REPORT],
            ["README.md", "Podsumowanie 2023", "pct", NEW_SUMMARY],
            [NEW_REPORT, "poprzednim rokiem", "angle", "../Archiwum/Lata ubiegłe/2023 – podsumowanie.md"],
            [NEW_SUMMARY, "sprawozdanie 2024", "pct", "../../Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md"],
            [NEW_SUMMARY, "spis treści", "plain", "../../index.md"],
        ],
    }
    argv = t.grade_script(GRADER_PRELUDE + "DATA = " + py_literal(data) + "\n" + d(r'''
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
        '''))
    t.solve_files({k: v for k, v in new.items() if files.get(k) != v}, delete=[OLD_REPORT, OLD_SUMMARY], answer=(
        "Renamed the 2024 report to `%s`, moved the 2023 summary to `%s`, and updated every link to them "
        "(README.md, docs/index.md, the invoice page, mkdocs.yml nav) plus the relative links inside the two "
        "moved files. Percent-encoded links stay percent-encoded and `<...>` links keep their angle brackets."
        % (NEW_REPORT, NEW_SUMMARY)))
    t.register(
        name="Rename and move Markdown files with Polish characters and spaces, fixing every link in its own style",
        description=("Paths with spaces, Polish diacritics and an en dash; links in three styles (percent-encoded, "
                     "<angle>, plain relative) including relative links inside the moved files and mkdocs nav. "
                     "Graded by resolving every link and checking each changed link kept its style."),
        category="windows",
        tags=["windows", "unicode-paths", "spaces", "markdown", "rename"],
        difficulty="medium",
        prompt=("Please reorganise the docs:\n"
                "1. Rename `docs/Raporty roczne/zażółć gęślą jaźń.md` to `docs/Raporty roczne/Sprawozdanie 2024 – wersja końcowa.md`.\n"
                "2. Move `docs/Raporty roczne/2023 – podsumowanie.md` into a new folder `docs/Archiwum/Lata ubiegłe/` (keep the file name).\n"
                "Then fix every reference so that all links in the Markdown files and the `mkdocs.yml` nav still resolve - "
                "including the relative links inside the two files you moved. Keep each link's existing style: "
                "percent-encoded links stay percent-encoded, links in `<...>` stay in angle brackets. "
                "Don't change anything else (no reformatting, no other content edits)."),
        expectedChangedFiles=[NEW_REPORT, NEW_SUMMARY, OLD_REPORT, OLD_SUMMARY, "README.md", "mkdocs.yml", "docs/index.md"],
        strictDiff=True,
        allowedChangedFiles=["docs/**", "README.md", "mkdocs.yml"],
        forbiddenChangedFiles=["docs/Polityka prywatności.md", "docs/Raporty roczne/załączniki/**"],
        verificationCommands=[cmd("links-and-paths", *argv)],
    )


# ---------------------------------------------------------------- CRLF + BOM

APP_INI = crlf(d('''
    ; Konfiguracja aplikacji – środowisko produkcyjne
    ; Plik czytany przez usługę Windows "AplikacjaSerwer" (UTF-8 z BOM).
    [server]
    host=0.0.0.0
    port=8080
    max_połączeń=200

    [logging]
    level=info
    plik=C:\\Logi\\Aplikacja\\serwer.log
    '''))

DEPLOY_PS1 = crlf(d('''
    <#
    .SYNOPSIS
      Wdraża aplikację do Azure. Uruchamiać w Windows PowerShell 5.1.
    #>
    param(
        [string]$Environment = 'prod'
    )

    $ErrorActionPreference = 'Stop'
    $Region = 'westeurope'
    $ResourceGroup = "rg-aplikacja-$Environment"

    Write-Host "Rozpoczynam wdrażanie do regionu $Region (grupa: $ResourceGroup)…"
    az group create --name $ResourceGroup --location $Region | Out-Null
    az deployment group create --resource-group $ResourceGroup --template-file "$PSScriptRoot\\main.bicep" | Out-Null
    Write-Host "Wdrażanie zakończone pomyślnie. Żółć i gęś są bezpieczne."
    '''))

SETTINGS_JSON = d('''
    {
      "name": "aplikacja-serwer",
      "port": 8080,
      "region": "westeurope",
      "features": {
        "audit": true,
        "legacyApi": false
      }
    }
    ''')

KLIENCI_CSV = crlf(d('''
    id;nazwa;miasto;nip
    1;Przedsiębiorstwo Łąka sp. z o.o.;Łódź;7251234567
    2;Źródło Wody S.A.;Kraków;6762345678
    3;"Gęś; Kaczka i Wspólnicy";Gdańsk;5833456789
    '''))

IMPORTER = d('''
    """Imports the customer list exported by the ERP (semicolon separated)."""
    import csv


    def load_customers(path):
        with open(path, encoding="utf-8", newline="") as f:
            rows = list(csv.DictReader(f, delimiter=";"))
        return [{"id": int(r["id"]), "name": r["nazwa"], "city": r["miasto"], "nip": r["nip"]} for r in rows]
    ''')

IMPORT_TEST = d('''
    import os
    import unittest

    from tools.import_klienci import load_customers

    HERE = os.path.dirname(os.path.abspath(__file__))


    class ImportTest(unittest.TestCase):
        def test_loads_all_customers(self):
            rows = load_customers(os.path.join(HERE, "..", "data", "klienci.csv"))
            self.assertEqual([r["id"] for r in rows], [1, 2, 3])
            self.assertEqual(rows[0]["city"], "Łódź")
            self.assertEqual(rows[2]["name"], "Gęś; Kaczka i Wspólnicy")


    if __name__ == "__main__":
        unittest.main()
    ''')


def build_crlf_bom():
    t = Task("win-crlf-bom")
    orig = {
        "config/app.ini": BOM + APP_INI.encode("utf-8"),
        "scripts/Deploy-App.ps1": BOM + DEPLOY_PS1.encode("utf-8"),
        "src/settings.json": SETTINGS_JSON.encode("utf-8"),
        "data/klienci.csv": BOM + KLIENCI_CSV.encode("utf-8"),
    }
    for rel, b in orig.items():
        t.fx(rel, b)
    t.fx("tools/__init__.py", "")
    t.fx("tools/import_klienci.py", IMPORTER)
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_import.py", IMPORT_TEST)
    t.fx("README.md", d('''
        # aplikacja-serwer

        Configuration and deployment scripts for the Windows service. Files in `config/`, `scripts/` and
        `data/` are read by Windows tools; see the comments in each file for the expected encoding.
        Tests: `python -m unittest discover -s tests -t .`
        '''))
    expected = {
        "config/app.ini": orig["config/app.ini"].replace(b"port=8080\r\n", b"port=8443\r\n"),
        "scripts/Deploy-App.ps1": orig["scripts/Deploy-App.ps1"].replace(b"$Region = 'westeurope'", b"$Region = 'polandcentral'"),
        "src/settings.json": orig["src/settings.json"].replace(b'"port": 8080', b'"port": 8443'),
        "data/klienci.csv": orig["data/klienci.csv"][len(BOM):],
    }
    for k in expected:
        assert expected[k] != orig[k], k
    import base64
    payload = {k: base64.b64encode(v).decode() for k, v in sorted(expected.items())}
    argv = t.grade_script(GRADER_PRELUDE + "import base64\nEXPECTED = " + py_literal(payload) + "\n" + d(r'''
        def describe(b):
            return "bom=%s crlf=%d lone_lf=%d len=%d" % (b.startswith(b"\xef\xbb\xbf"), b.count(b"\r\n"),
                                                      b.count(b"\n") - b.count(b"\r\n"), len(b))

        for rel, enc in sorted(EXPECTED.items()):
            want = base64.b64decode(enc)
            try:
                got = read_bytes(rel)
            except OSError as e:
                check(rel + " exact bytes", False, e)
                continue
            if got == want:
                check(rel + " exact bytes", True)
                continue
            # Explain the most common failure modes.
            detail = "got " + describe(got) + " want " + describe(want)
            if norm_text(got) == norm_text(want):
                detail += " (content right, encoding/line endings/BOM wrong)"
            check(rel + " exact bytes", False, detail)
        finish()
        '''))
    t.solve_files(expected, answer=(
        "Changed the port to 8443 in config/app.ini and src/settings.json and the region to polandcentral in "
        "scripts/Deploy-App.ps1, keeping each file's encoding and CRLF/LF line endings (the .ps1 and .ini keep "
        "their UTF-8 BOM). Removed the UTF-8 BOM from data/klienci.csv (CRLF kept); the importer test passes now."))
    t.register(
        name="Edit CRLF and UTF-8-BOM files byte-exactly, and strip one BOM on request",
        description=("Three small value changes in files with different encodings (BOM+CRLF .ini and .ps1, LF "
                     "JSON) that must keep their exact bytes otherwise, plus removing a BOM that breaks a CSV "
                     "importer. Graded by exact byte comparison and the importer test."),
        category="windows",
        tags=["windows", "crlf", "bom", "encoding", "powershell"],
        difficulty="medium",
        prompt=("We are moving production to the Poland Central region. Please:\n"
                "1. In `config/app.ini` change the server port from 8080 to 8443.\n"
                "2. In `scripts/Deploy-App.ps1` change the region from `westeurope` to `polandcentral` (only the `$Region` assignment).\n"
                "3. In `src/settings.json` change `port` to 8443 too (leave its `region` field alone, another team owns it).\n"
                "These files are consumed by Windows tooling, so keep each file's encoding and line endings exactly as they are - "
                "`Deploy-App.ps1` must keep its UTF-8 BOM, Windows PowerShell 5.1 garbles the Polish text without it.\n"
                "4. The importer test fails because `data/klienci.csv` starts with a UTF-8 BOM. Remove the BOM from that file "
                "(everything else in it byte-for-byte unchanged, CRLF line endings included). Don't change the importer or the tests, and don't leave helper scripts in the repo."),
        expectedChangedFiles=sorted(expected),
        strictDiff=True,
        allowedChangedFiles=sorted(expected),
        forbiddenChangedFiles=["tools/**", "tests/**"],
        verificationCommands=[
            cmd("importer-test", "python", "-m", "unittest", "discover", "-s", "tests", "-t", "."),
            cmd("exact-bytes", *argv, weight=3),
        ],
    )


# ---------------------------------------------------------------- long paths

CLIENTS = [
    {"id": "K-017", "nip": "7251803344",
     "nazwa": "Przedsiębiorstwo Produkcyjno-Handlowo-Usługowe Zielony Gaj Spółka z ograniczoną odpowiedzialnością spółka komandytowa"},
    {"id": "K-018", "nip": "7251803345", "nazwa": "Zielony Gaj Serwis sp. z o.o. oddział Kraków"},
    {"id": "K-042", "nip": "6793120098",
     "nazwa": "Międzynarodowe Towarzystwo Ubezpieczeń Wzajemnych Rolników i Sadowników Południowej Małopolski"},
    {"id": "K-105", "nip": "5862200417",
     "nazwa": "Spółdzielnia Mieszkaniowa Lokatorsko-Własnościowa Przyszłość im. Józefa Wybickiego w Gdańsku-Wrzeszczu"},
]

DOCS = [
    ("UM-2024-0173", "K-017", 2024, "umowa ramowa na dostawy materiałów biurowych i eksploatacyjnych"),
    ("AN-2023-0042", "K-042", 2023, "aneks nr 3 do umowy o świadczenie usług ubezpieczeniowych floty pojazdów"),
    ("UM-2025-0007", "K-105", 2025, "umowa o administrowanie nieruchomościami wspólnymi osiedla Przyszłość"),
]


def archive_path(doc):
    num, cid, year, title = doc
    name = next(c["nazwa"] for c in CLIENTS if c["id"] == cid)
    return "Archiwum umów/%s/%d/%s – %s.md" % (name, year, num, title)


def build_longpath():
    t = Task("win-longpath-archive")
    t.classic_ref = False  # reference files/ would exceed MAX_PATH inside the repository checkout
    t.fx("klienci.json", json.dumps(CLIENTS, ensure_ascii=False, indent=2) + "\n")
    t.fx("ARCHIWUM.md", d('''
        # Archiwum umów - zasady

        Każdy dokument z `do_archiwizacji/` trafia do:

            Archiwum umów/<pełna nazwa klienta>/<rok>/<numer> – <tytuł>.md

        - `<pełna nazwa klienta>`: pole `nazwa` z `klienci.json` dla identyfikatora z pola `klient`, bez zmian
          (wielkość liter, spacje i polskie znaki zostają).
        - `<rok>`, `<numer>`, `<tytuł>`: pola `rok`, `numer`, `tytuł` z nagłówka dokumentu.
        - Między numerem a tytułem: spacja, półpauza (–, U+2013), spacja.
        - Treść pliku nie może się zmienić ani o bajt (podpisy elektroniczne liczą skrót z całego pliku).

        Przykład: dokument `XX-2022-0001` klienta `K-018` z roku 2022 o tytule `umowa testowa` trafia do
        `Archiwum umów/Zielony Gaj Serwis sp. z o.o. oddział Kraków/2022/XX-2022-0001 – umowa testowa.md`.
        '''))
    originals = {}
    for num, cid, year, title in DOCS:
        body = crlf(d('''
            ---
            numer: %s
            klient: %s
            rok: %d
            tytuł: %s
            ---

            # %s

            Strony ustalają, co następuje: § 1. Przedmiot umowy opisano w załączniku nr 1.
            § 2. Wynagrodzenie płatne w terminie 14 dni od doręczenia faktury.

            Podpisano kwalifikowanym podpisem elektronicznym.
            ''') % (num, cid, year, title, title.capitalize()))
        rel = "do_archiwizacji/%s.md" % num
        originals[rel] = body.encode("utf-8")
        t.fx(rel, body)
    targets = {archive_path(doc): originals["do_archiwizacji/%s.md" % doc[0]] for doc in DOCS}
    longest = max(len(p) for p in targets)
    assert longest > 160, longest  # with the ~110+ char workspace prefix this exceeds MAX_PATH (260)
    import base64
    payload = {k: base64.b64encode(v).decode() for k, v in sorted(targets.items())}
    argv = t.grade_script(GRADER_PRELUDE + "import base64\nTARGETS = " + py_literal(payload) + "\n" + d(r'''
        import unicodedata

        NFC = lambda s: unicodedata.normalize("NFC", s)
        found = {}
        root = "Archiwum umów"
        if os.path.isdir(lp(root)):
            for base, dirs, files in os.walk(lp(root)):
                for n in files:
                    full = os.path.join(base, n)
                    rel = os.path.relpath(full, lp(".")).replace(os.sep, "/")
                    found[NFC(rel)] = full
        check("archive folder exists", bool(found))
        for rel, enc in sorted(TARGETS.items()):
            want = base64.b64decode(enc)
            full = found.get(NFC(rel))
            if not check("archived: " + rel, full is not None, "have: %s" % sorted(found)[:5]):
                continue
            with open(full, "rb") as f:
                got = f.read()
            check("byte-identical: " + rel, got == want,
                  "content differs" + (" (line endings/BOM only)" if norm_text(got) == norm_text(want) else ""))
        check("no extra files in the archive", sorted(found) == sorted(NFC(r) for r in TARGETS),
              sorted(set(found) - set(NFC(r) for r in TARGETS))[:5])
        left = []
        if os.path.isdir("do_archiwizacji"):
            for base, _, files in os.walk("do_archiwizacji"):
                left += files
        check("do_archiwizacji emptied", not left, left)
        finish()
        '''))
    t.solve_files(targets, delete=sorted(originals), answer=(
        "Archived the three documents under `Archiwum umów/<client>/<year>/<number> – <title>.md` (byte-identical "
        "moves; the longest path is %d characters relative to the project) and emptied `do_archiwizacji/`." % longest))
    t.register(
        name="Archive documents into paths longer than MAX_PATH (Polish names, spaces, en dash)",
        description=("Move three CRLF documents into an archive layout derived from a JSON client list; the "
                     "resulting absolute paths exceed 260 characters in the evalsuite workspace. Graded with "
                     "long-path-aware Python: exact target names, byte-identical content, source emptied."),
        category="windows",
        tags=["windows", "long-paths", "unicode-paths", "file-moves"],
        difficulty="hard",
        prompt=("Please archive the three documents in `do_archiwizacji/` following the rules in `ARCHIWUM.md` "
                "(target folder and file name built from `klienci.json` and each document's header). Move the files - "
                "their content must stay byte-for-byte identical - so that `do_archiwizacji/` ends up empty. "
                "Heads-up: the full client names make the resulting Windows paths very long; make sure the files "
                "really end up at the exact archive paths. Don't leave helper scripts in the repo."),
        strictDiff=True,
        allowedChangedFiles=["do_archiwizacji/**", "Archiwum umów/**"],
        forbiddenChangedFiles=["klienci.json", "ARCHIWUM.md"],
        verificationCommands=[cmd("archive-layout", *argv)],
    )


def build():
    build_polish_paths()
    build_crlf_bom()
    build_longpath()
