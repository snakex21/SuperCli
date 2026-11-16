"""Imports the customer list exported by the ERP (semicolon separated)."""
import csv


def load_customers(path):
    with open(path, encoding="utf-8", newline="") as f:
        rows = list(csv.DictReader(f, delimiter=";"))
    return [{"id": int(r["id"]), "name": r["nazwa"], "city": r["miasto"], "nip": r["nip"]} for r in rows]
