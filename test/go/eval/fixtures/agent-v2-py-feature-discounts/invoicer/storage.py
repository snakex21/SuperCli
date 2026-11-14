"""JSON persistence for invoices."""
import json

from .models import Invoice, LineItem


def invoice_to_dict(inv):
    return {
        "number": inv.number,
        "customer": inv.customer,
        "tax_rate_bp": inv.tax_rate_bp,
        "items": [{"sku": i.sku, "qty": i.qty, "unit_cents": i.unit_cents} for i in inv.items],
    }


def invoice_from_dict(data):
    return Invoice(
        number=data["number"],
        customer=data["customer"],
        tax_rate_bp=data.get("tax_rate_bp", 0),
        items=[LineItem(**i) for i in data.get("items", [])],
    )


def load_invoice(path):
    with open(path, encoding="utf-8") as f:
        return invoice_from_dict(json.load(f))


def save_invoice(inv, path):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(invoice_to_dict(inv), f, indent=2)
