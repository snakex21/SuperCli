"""Load CSV files into sheets."""

import csv

from sheetcalc.engine import Sheet


def _cell(text):
    text = text.strip()
    if text.startswith("="):
        return text
    try:
        return float(text)
    except ValueError:
        return text


def load_csv(path):
    """Sheet with the CSV contents; numeric text becomes numbers."""
    sheet = Sheet()
    with open(path, newline="", encoding="utf-8") as f:
        for r, row in enumerate(csv.reader(f)):
            for c, text in enumerate(row):
                if text.strip():
                    sheet.set_rc(r, c, _cell(text))
    return sheet
