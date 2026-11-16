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
 "odsetki/kalkulator.py": "IiIiT2RzZXRraSB1c3Rhd293ZSB6YSBvcMOzxbpuaWVuaWUgdyB0cmFuc2FrY2phY2ggaGFuZGxvd3ljaC4KCk9kc2V0a2kgbmFsaWN6YSBzacSZIHphIGthxbxkeSBkemllxYQgb3DDs8W6bmllbmlhOiBvZCBkbmlhIG5hc3TEmXB1asSFY2VnbyBwbwp0ZXJtaW5pZSBwxYJhdG5vxZtjaSBkbyBkbmlhIHphcMWCYXR5IHfFgsSFY3puaWUsIHByenlqbXVqxIVjIHJvayAzNjUtZG5pb3d5Cih0YWvFvGUgdyBsYXRhY2ggcHJ6ZXN0xJlwbnljaCkuCiIiIgpmcm9tIGRhdGV0aW1lIGltcG9ydCBkYXRlCmZyb20gZGVjaW1hbCBpbXBvcnQgUk9VTkRfSEFMRl9VUCwgRGVjaW1hbAoKRE5JX1dfUk9LVSA9IDM2NQpHUk9TWiA9IERlY2ltYWwoIjAuMDEiKQoKCmRlZiBkbmlfb3Bvem5pZW5pYSh0ZXJtaW46IGRhdGUsIHphcGxhdGE6IGRhdGUpIC0+IGludDoKICAgICIiIkxpY3piYSBkbmkgb3DDs8W6bmllbmlhICgwLCBnZHkgemFwxYJhY29ubyB3IHRlcm1pbmllKS4iIiIKICAgIHJldHVybiBtYXgoKHphcGxhdGEgLSB0ZXJtaW4pLmRheXMsIDApCgoKZGVmIG5hbGljel9vZHNldGtpKGt3b3RhLCBzdG9wYV9yb2N6bmEsIHRlcm1pbjogZGF0ZSwgemFwbGF0YTogZGF0ZSkgLT4gRGVjaW1hbDoKICAgICIiIk9kc2V0a2kgb2Qga3dvdHkgcHJ6eSByb2N6bmVqIHN0b3BpZSAobnAuIERlY2ltYWwoIjAuMTEyNSIpID0gMTEsMjUlKS4iIiIKICAgIGRuaSA9IGRuaV9vcG96bmllbmlhKHRlcm1pbiwgemFwbGF0YSkKICAgIG9kc2V0a2kgPSBEZWNpbWFsKGt3b3RhKSAqIERlY2ltYWwoc3RvcGFfcm9jem5hKSAqIGRuaSAvIEROSV9XX1JPS1UKICAgIHJldHVybiBvZHNldGtpLnF1YW50aXplKEdST1NaLCByb3VuZGluZz1ST1VORF9IQUxGX1VQKQoKCmRlZiBkb196YXBsYXR5KGt3b3RhLCBzdG9wYV9yb2N6bmEsIHRlcm1pbjogZGF0ZSwgemFwbGF0YTogZGF0ZSkgLT4gRGVjaW1hbDoKICAgICIiIkt3b3RhIGfFgsOzd25hIHBvd2nEmWtzem9uYSBvIG9kc2V0a2kuIiIiCiAgICByZXR1cm4gKERlY2ltYWwoa3dvdGEpICsgbmFsaWN6X29kc2V0a2koa3dvdGEsIHN0b3BhX3JvY3puYSwgdGVybWluLCB6YXBsYXRhKSkucXVhbnRpemUoR1JPU1opCg=="
}
DELETE = []
ANSWER = "Przyczyn\u0105 by\u0142 b\u0142\u0105d w sta\u0142ej `DNI_W_ROKU`: kod dzieli\u0142 przez 360 dni, a zgodnie z opisem modu\u0142u (i oczekiwaniem testu) odsetki liczy si\u0119 przy roku 365-dniowym. Dlatego dla 10 000 z\u0142, 11,25% i 30 dni wychodzi\u0142o 93,75 z\u0142 zamiast 92,47 z\u0142. Zmieni\u0142em `DNI_W_ROKU` z 360 na 365 w `odsetki/kalkulator.py`; test\u00f3w nie rusza\u0142em. Wszystkie testy przechodz\u0105."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
