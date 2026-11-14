"""Money arithmetic. All amounts are integer cents."""


def round_half_up(numer, denom):
    """Integer numer/denom rounded half up (both non-negative)."""
    return (2 * numer + denom) // (2 * denom)


def subtotal_cents(inv):
    return sum(item.qty * item.unit_cents for item in inv.items)


def tax_cents(amount_cents, rate_bp):
    return round_half_up(amount_cents * rate_bp, 10000)


def discount_cents(inv, discounts):
    code = getattr(inv, "discount_code", None)
    if not code:
        return 0
    table = {k.upper(): v for k, v in (discounts or {}).items()}
    disc = table.get(code.upper())
    if disc is None:
        raise KeyError("unknown discount code: %s" % code)
    sub = subtotal_cents(inv)
    if sub < disc.min_subtotal_cents:
        return 0
    if disc.percent is not None:
        return round_half_up(sub * disc.percent, 100)
    return min(disc.amount_cents, sub)


def total_cents(inv, discounts=None):
    taxable = subtotal_cents(inv) - discount_cents(inv, discounts)
    return taxable + tax_cents(taxable, inv.tax_rate_bp)
