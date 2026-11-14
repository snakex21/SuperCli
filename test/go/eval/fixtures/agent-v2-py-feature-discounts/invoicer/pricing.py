"""Money arithmetic. All amounts are integer cents."""


def round_half_up(numer, denom):
    """Integer numer/denom rounded half up (both non-negative)."""
    return (2 * numer + denom) // (2 * denom)


def subtotal_cents(inv):
    return sum(item.qty * item.unit_cents for item in inv.items)


def tax_cents(amount_cents, rate_bp):
    return round_half_up(amount_cents * rate_bp, 10000)


def total_cents(inv):
    sub = subtotal_cents(inv)
    return sub + tax_cents(sub, inv.tax_rate_bp)
