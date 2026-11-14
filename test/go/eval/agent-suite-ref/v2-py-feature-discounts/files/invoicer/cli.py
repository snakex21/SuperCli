"""Command line: python -m invoicer.cli total INVOICE.json"""
import argparse
import sys

from .formatting import fmt_cents
from .pricing import discount_cents, subtotal_cents, tax_cents
from .storage import load_discounts, load_invoice


def main(argv=None, out=sys.stdout):
    parser = argparse.ArgumentParser(prog="invoicer")
    sub = parser.add_subparsers(dest="cmd", required=True)
    total = sub.add_parser("total", help="print subtotal, tax and total")
    total.add_argument("invoice")
    total.add_argument("--discounts")
    args = parser.parse_args(argv)
    if args.cmd == "total":
        inv = load_invoice(args.invoice)
        discounts = load_discounts(args.discounts) if args.discounts else {}
        s = subtotal_cents(inv)
        disc = discount_cents(inv, discounts)
        tax = tax_cents(s - disc, inv.tax_rate_bp)
        print("Subtotal: " + fmt_cents(s), file=out)
        if disc:
            print("Discount (%s): -%s" % (inv.discount_code.upper(), fmt_cents(disc)), file=out)
        print("Tax: " + fmt_cents(tax), file=out)
        print("Total: " + fmt_cents(s - disc + tax), file=out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
