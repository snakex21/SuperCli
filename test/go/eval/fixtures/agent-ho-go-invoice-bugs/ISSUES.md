# Open issues

Reported by customers and support; each needs a fix. Expected behaviour is
what the issue says.

## #101 Credit amounts look broken

A credit of -0.05 is printed as `0.-5` and -1.05 as `-1.-5`. Negative
amounts must print with a leading minus and two decimals: `-0.05`,
`-1.05`, `-1234.50`.

## #102 Import fails for lower-case country codes

Country codes from the import arrive as `de`, ` pl ` and so on. The VAT
lookup must ignore case and surrounding spaces. Unknown codes must still be
an error.

## #103 100 units only get 10%

Ordering exactly 100 units gives a 10% discount; the price list says 100 or
more units get 15%.

## #104 Sunday due dates land on Tuesday

An invoice whose due date falls on a Sunday is now due on Tuesday. It must
move to the following Monday (Saturday already works).

## #105 VAT is charged on the undiscounted amount

VAT must be computed on the net amount after the quantity discount:
net = sum of lines - discount, VAT = tax on net, gross = net + VAT.
Example: DE, 120 units at 10.00 -> net 1020.00, VAT 193.80, gross 1213.80.

## #106 Descriptions with commas cannot be imported

`3,W-1,"Widget, large",2,19.99` fails with `want 5 fields, got 6`. The
importer must accept standard CSV quoting (quoted fields may contain
commas and doubled quotes).

## #107 Invoice numbers have the wrong width

Invoice numbers must have six digits (INV-000042); they have five.
Credit note numbers (CN-0042) are correct and must not change.

## #108 Line 10 is printed before line 2

The text rendering must list lines in numeric order of their line number.
