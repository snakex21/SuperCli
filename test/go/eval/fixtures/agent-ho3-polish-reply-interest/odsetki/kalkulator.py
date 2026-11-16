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
