"""Order pricing. Amounts are Decimal values in PLN."""
from decimal import ROUND_HALF_UP, Decimal

CENT = Decimal("0.01")


def money(x):
    """Round to grosze, half up."""
    return Decimal(x).quantize(CENT, rounding=ROUND_HALF_UP)


def order_total(lines, vat_rate=Decimal("0")):
    """Gross total of (unit_price, qty) lines, rounded to grosze.

    VAT is computed per line on the net line amount and rounded per line.
    """
    total = Decimal("0")
    for unit_price, qty in lines:
        net = money(Decimal(unit_price) * qty)
        total += money(net * (1 + Decimal(vat_rate)))
    return money(total)
