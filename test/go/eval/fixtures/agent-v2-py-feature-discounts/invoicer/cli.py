"""Command line: python -m invoicer.cli total INVOICE.json"""
import argparse
import sys

from .formatting import fmt_cents
from .pricing import subtotal_cents, tax_cents
from .storage import load_invoice


def main(argv=None, out=sys.stdout):
    parser = argparse.ArgumentParser(prog="invoicer")
    sub = parser.add_subparsers(dest="cmd", required=True)
    total = sub.add_parser("total", help="print subtotal, tax and total")
    total.add_argument("invoice")
    args = parser.parse_args(argv)
    if args.cmd == "total":
        inv = load_invoice(args.invoice)
        s = subtotal_cents(inv)
        tax = tax_cents(s, inv.tax_rate_bp)
        print("Subtotal: " + fmt_cents(s), file=out)
        print("Tax: " + fmt_cents(tax), file=out)
        print("Total: " + fmt_cents(s + tax), file=out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
