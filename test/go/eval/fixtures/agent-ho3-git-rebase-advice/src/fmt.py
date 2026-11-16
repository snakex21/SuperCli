"""Number formatting."""


def amount(value, currency=""):
    text = "%.2f" % value
    return (text + " " + currency).strip()
