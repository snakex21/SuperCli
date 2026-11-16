import os
import sys

sys.dont_write_bytecode = True
try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass


def lp(path):
    """Long-path safe absolute path on Windows."""
    p = os.path.abspath(path)
    if os.name == "nt" and not p.startswith("\\\\?\\"):
        p = "\\\\?\\" + p
    return p


def rb(path):
    with open(path, "rb") as f:
        return f.read()


def wb(path, data):
    d = os.path.dirname(path)
    if d:
        os.makedirs(d, exist_ok=True)
    if isinstance(data, str):
        data = data.encode("utf-8")
    with open(path, "wb") as f:
        f.write(data)


def replace(path, old, new, count=1):
    data = rb(path)
    old_b = old.encode("utf-8") if isinstance(old, str) else old
    new_b = new.encode("utf-8") if isinstance(new, str) else new
    if data.count(old_b) < 1:
        sys.exit("solve: %s: pattern not found: %r" % (path, old_b[:60]))
    wb(path, data.replace(old_b, new_b, count) if count else data.replace(old_b, new_b))
import base64

FILES = {
 "output/miesiecznie.csv": "bWllc2lhYyxrbGllbnQsc3VtYV9wbG4KMjAyNi0wMSwiQXB0ZWthICIiUG9kIE9yxYJlbSIiIiwxMDc3NDEuMDcKMjAyNi0wMSxCxIVrIFRvbWFzeiwyMDEyNS41NQoyMDI2LTAxLEh1cnRvd25pYSDFgcSFa2Egc3AuIHogby5vLiw0NDg2My44MgoyMDI2LTAxLCJLb3dhbHNraSwgSmFuIiwxNzg4MS40OAoyMDI2LTAxLFppZWxvbnkgR2FqLDEyODQuODIKMjAyNi0wMSzEhm1hIGkgU3luLDIyOTQzLjUwCjIwMjYtMDEsxblyw7NkxYJvIFMuQS4sNDg0LjE1CjIwMjYtMDIsIkFwdGVrYSAiIlBvZCBPcsWCZW0iIiIsMzA2LjE0CjIwMjYtMDIsQsSFayBUb21hc3osOTk4LjY0CjIwMjYtMDIsSHVydG93bmlhIMWBxIVrYSBzcC4geiBvLm8uLDMyNTguNDcKMjAyNi0wMiwiS293YWxza2ksIEphbiIsMzcwLjA5CjIwMjYtMDIsWklFTE9OWSBHQUosNjc0OTIuMTkKMjAyNi0wMixaaWVsb255IEdhaiw1MTE0My42OQoyMDI2LTAyLMSGbWEgaSBTeW4sNTU3LjA2CjIwMjYtMDIsxblyw7NkxYJvIFMuQS4sMjE1LjgyCjIwMjYtMDMsIkFwdGVrYSAiIlBvZCBPcsWCZW0iIiIsNzQ0MC4wOQoyMDI2LTAzLELEhWsgVG9tYXN6LDIyODQ4LjQyCjIwMjYtMDMsSHVydG93bmlhIMWBxIVrYSBzcC4geiBvLm8uLDI0MTk2LjYxCjIwMjYtMDMsIktvd2Fsc2tpLCBKYW4iLDI0ODI5LjM0CjIwMjYtMDMsWmllbG9ueSBHYWosNDQxMTQuNTMKMjAyNi0wMyzFuXLDs2TFgm8gUy5BLiw2OS4yNgo=",
 "output/summary.json": "ewogICJjdXN0b21lcnMiOiBbCiAgICB7CiAgICAgICJrbGllbnQiOiAiQXB0ZWthIFwiUG9kIE9yxYJlbVwiIiwKICAgICAgInRyYW5zYWtjamUiOiA3LAogICAgICAic3VtYV9nciI6IDExNTQ4NzMwLAogICAgICAiendyb3R5X2dyIjogMzk5OAogICAgfSwKICAgIHsKICAgICAgImtsaWVudCI6ICJaaWVsb255IEdhaiIsCiAgICAgICJ0cmFuc2FrY2plIjogNiwKICAgICAgInN1bWFfZ3IiOiA5NjU0MzA0LAogICAgICAiendyb3R5X2dyIjogNTAwMAogICAgfSwKICAgIHsKICAgICAgImtsaWVudCI6ICJIdXJ0b3duaWEgxYHEhWthIHNwLiB6IG8uby4iLAogICAgICAidHJhbnNha2NqZSI6IDcsCiAgICAgICJzdW1hX2dyIjogNzIzMTg5MCwKICAgICAgInp3cm90eV9nciI6IDAKICAgIH0sCiAgICB7CiAgICAgICJrbGllbnQiOiAiWklFTE9OWSBHQUoiLAogICAgICAidHJhbnNha2NqZSI6IDUsCiAgICAgICJzdW1hX2dyIjogNjc0OTIxOSwKICAgICAgInp3cm90eV9nciI6IDAKICAgIH0sCiAgICB7CiAgICAgICJrbGllbnQiOiAiQsSFayBUb21hc3oiLAogICAgICAidHJhbnNha2NqZSI6IDMsCiAgICAgICJzdW1hX2dyIjogNDM5NzI2MSwKICAgICAgInp3cm90eV9nciI6IDAKICAgIH0sCiAgICB7CiAgICAgICJrbGllbnQiOiAiS293YWxza2ksIEphbiIsCiAgICAgICJ0cmFuc2FrY2plIjogNSwKICAgICAgInN1bWFfZ3IiOiA0MzA4MDkxLAogICAgICAiendyb3R5X2dyIjogMAogICAgfSwKICAgIHsKICAgICAgImtsaWVudCI6ICLEhm1hIGkgU3luIiwKICAgICAgInRyYW5zYWtjamUiOiAyLAogICAgICAic3VtYV9nciI6IDIzNTAwNTYsCiAgICAgICJ6d3JvdHlfZ3IiOiAwCiAgICB9LAogICAgewogICAgICAia2xpZW50IjogIsW5csOzZMWCbyBTLkEuIiwKICAgICAgInRyYW5zYWtjamUiOiA1LAogICAgICAic3VtYV9nciI6IDc2OTIzLAogICAgICAiendyb3R5X2dyIjogMTk5OQogICAgfQogIF0sCiAgInJhemVtX2dyIjogNDYzMTY0NzQsCiAgInBvbWluaWV0ZSI6IHsKICAgICJhbnVsb3dhbmUiOiA5LAogICAgIm9jemVrdWphY2UiOiA1LAogICAgImR1cGxpa2F0eSI6IDYKICB9Cn0K"
}
DELETE = []
ANSWER = "Generated output/summary.json (8 customers, razem_gr=46316474; skipped: {'anulowane': 9, 'oczekujace': 5, 'duplikaty': 6}) and output/miesiecznie.csv (21 rows) following SPEC.md: first occurrence of each id only, ZAKSI\u0118GOWANA rows only, Polish amounts parsed (space / no-break space thousands, decimal comma), EUR converted per month with round-half-up to grosze."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
