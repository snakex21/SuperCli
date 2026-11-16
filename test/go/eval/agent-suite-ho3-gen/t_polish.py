"""User-language reply: a Polish prompt about a failing test must get a Polish answer."""
from common import GRADER_PRELUDE, Task, cmd, d, sub

KALKULATOR = d('''
    """Odsetki ustawowe za opóźnienie w transakcjach handlowych.

    Odsetki nalicza się za każdy dzień opóźnienia: od dnia następującego po
    terminie płatności do dnia zapłaty włącznie, przyjmując rok 365-dniowy
    (także w latach przestępnych).
    """
    from datetime import date
    from decimal import ROUND_HALF_UP, Decimal

    DNI_W_ROKU = 360
    GROSZ = Decimal("0.01")


    def dni_opoznienia(termin: date, zaplata: date) -> int:
        """Liczba dni opóźnienia (0, gdy zapłacono w terminie)."""
        return max((zaplata - termin).days, 0)


    def nalicz_odsetki(kwota, stopa_roczna, termin: date, zaplata: date) -> Decimal:
        """Odsetki od kwoty przy rocznej stopie (np. Decimal("0.1125") = 11,25%)."""
        dni = dni_opoznienia(termin, zaplata)
        odsetki = Decimal(kwota) * Decimal(stopa_roczna) * dni / DNI_W_ROKU
        return odsetki.quantize(GROSZ, rounding=ROUND_HALF_UP)


    def do_zaplaty(kwota, stopa_roczna, termin: date, zaplata: date) -> Decimal:
        """Kwota główna powiększona o odsetki."""
        return (Decimal(kwota) + nalicz_odsetki(kwota, stopa_roczna, termin, zaplata)).quantize(GROSZ)
    ''')

TESTS = d('''
    import unittest
    from datetime import date
    from decimal import Decimal

    from odsetki.kalkulator import do_zaplaty, dni_opoznienia, nalicz_odsetki


    class KalkulatorTest(unittest.TestCase):
        def test_dni_opoznienia(self):
            self.assertEqual(dni_opoznienia(date(2025, 1, 31), date(2025, 3, 2)), 30)
            self.assertEqual(dni_opoznienia(date(2025, 1, 31), date(2025, 1, 20)), 0)

        def test_zaplata_w_terminie(self):
            self.assertEqual(nalicz_odsetki("1000.00", "0.1125", date(2025, 5, 5), date(2025, 5, 5)), Decimal("0.00"))

        def test_nalicz_odsetki_rok(self):
            # 10 000 zł, 11,25% rocznie, 30 dni opóźnienia, rok 365-dniowy
            self.assertEqual(nalicz_odsetki("10000.00", "0.1125", date(2025, 1, 31), date(2025, 3, 2)), Decimal("92.47"))


    if __name__ == "__main__":
        unittest.main()
    ''')

NOT_LETTER = r"(^|[^\p{L}])"
END = r"($|[^\p{L}])"


def build():
    t = Task("polish-reply-interest")
    t.fx("odsetki/__init__.py", "")
    t.fx("odsetki/kalkulator.py", KALKULATOR)
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_kalkulator.py", TESTS)
    t.fx("README.md", "# odsetki\n\nKalkulator odsetek za opóźnienie. Testy: `python -m unittest discover -s tests -t .`\n")
    argv = t.grade_script(GRADER_PRELUDE + d(r'''
        from datetime import date
        from decimal import ROUND_HALF_UP, Decimal

        sys.path.insert(0, os.getcwd())
        from odsetki.kalkulator import do_zaplaty, nalicz_odsetki  # noqa: E402

        def want(kwota, stopa, termin, zaplata):
            dni = max((zaplata - termin).days, 0)
            return (Decimal(kwota) * Decimal(stopa) * dni / 365).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)

        cases = [("10000.00", "0.1125", date(2025, 1, 31), date(2025, 3, 2)),
                 ("5000.00", "0.1150", date(2024, 2, 1), date(2024, 3, 1)),  # leap year: still 365
                 ("1234.56", "0.0825", date(2025, 12, 15), date(2026, 2, 14)),
                 ("99.99", "0.1125", date(2025, 6, 1), date(2025, 6, 2))]
        for c in cases:
            got = nalicz_odsetki(*c)
            check("nalicz_odsetki%s" % (c[:2],), got == want(*c), (str(got), str(want(*c))))
        check("do_zaplaty", do_zaplaty("10000.00", "0.1125", date(2025, 1, 31), date(2025, 3, 2)) == Decimal("10092.47"))
        finish()
        '''))
    t.solve_files({"odsetki/kalkulator.py": sub(KALKULATOR, ("DNI_W_ROKU = 360\n", "DNI_W_ROKU = 365\n"))}, answer=(
        "Przyczyną był błąd w stałej `DNI_W_ROKU`: kod dzielił przez 360 dni, a zgodnie z opisem modułu (i oczekiwaniem "
        "testu) odsetki liczy się przy roku 365-dniowym. Dlatego dla 10 000 zł, 11,25% i 30 dni wychodziło 93,75 zł "
        "zamiast 92,47 zł. Zmieniłem `DNI_W_ROKU` z 360 na 365 w `odsetki/kalkulator.py`; testów nie ruszałem. "
        "Wszystkie testy przechodzą."))
    polish_words = NOT_LETTER + r"(się|że|jest|był|była|było|nie|został|została|zostało|zamiast|dlatego|ponieważ|" \
        r"przyczyn\p{L}*|zmieni\p{L}*|poprawi\p{L}*|test\p{L}* nie)" + END
    english = NOT_LETTER + r"(the|because|which|was|were|should|instead)" + END
    t.register(
        name="Polish prompt about a failing test must get a Polish answer (and a correct fix)",
        description=("The user writes in Polish; the bug (360-day year instead of the documented 365) must be "
                     "fixed without touching tests, and the explanation must be in Polish: Polish diacritics and "
                     "function words required, common English function words forbidden."),
        category="language",
        tags=["user-language", "polish", "python", "debugging"],
        difficulty="easy",
        prompt=("Cześć! Test `test_nalicz_odsetki_rok` w `tests/test_kalkulator.py` nie przechodzi. "
                "Znajdź przyczynę i popraw kod (testów nie zmieniaj). "
                "Odpowiedz mi po polsku: krótko, co było przyczyną błędu i co dokładnie zmieniłeś."),
        expectedChangedFiles=["odsetki/kalkulator.py"],
        forbiddenChangedFiles=["tests/**"],
        strictDiff=True,
        allowedChangedFiles=["odsetki/**"],
        verificationCommands=[
            cmd("unit-tests", "python", "-m", "unittest", "discover", "-s", "tests", "-t", "."),
            cmd("hidden-cases", *argv),
        ],
        answerChecks=[
            {"id": "polish-diacritics", "anyOf": [r"[ąćęłńóśźżĄĆĘŁŃÓŚŹŻ]"], "regex": True, "critical": True},
            {"id": "polish-function-words", "anyOf": [polish_words], "regex": True, "critical": True},
            {"id": "not-english", "noneOf": [english], "regex": True, "critical": True},
            {"id": "names-the-cause", "anyOf": ["365"], "weight": 2},
        ],
    )
