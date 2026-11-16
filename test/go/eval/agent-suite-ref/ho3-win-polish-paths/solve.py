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
 "README.md": "IyBEb2t1bWVudGFjamEgc3DDs8WCa2kKCk5handhxbxuaWVqc3plIGRva3VtZW50eToKCi0gW1NwcmF3b3pkYW5pZSByb2N6bmVdKDxkb2NzL1JhcG9ydHkgcm9jem5lL1NwcmF3b3pkYW5pZSAyMDI0IOKAkyB3ZXJzamEga2/FhGNvd2EubWQ+KQotIFtQb2RzdW1vd2FuaWUgMjAyM10oZG9jcy9BcmNoaXd1bS9MYXRhJTIwdWJpZWclQzUlODJlLzIwMjMlMjAlRTIlODAlOTMlMjBwb2RzdW1vd2FuaWUubWQpCi0gW1BvbGl0eWthIHByeXdhdG5vxZtjaV0oPGRvY3MvUG9saXR5a2EgcHJ5d2F0bm/Fm2NpLm1kPikKClN0cm9uYSBkb2t1bWVudGFjamkgYnVkb3dhbmEgamVzdCBwcnpleiBgbWtkb2NzIGJ1aWxkYCAoa29uZmlndXJhY2phIHcgYG1rZG9jcy55bWxgKS4K",
 "docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md": "IyBQb2RzdW1vd2FuaWUgMjAyMwoKUm9rIHphbWtuacSZdHkgenlza2llbSBuZXR0byAyLDQgbWxuIHrFgi4KCk5hc3TEmXBueSByYXBvcnQ6IFtzcHJhd296ZGFuaWUgMjAyNF0oLi4vLi4vUmFwb3J0eSUyMHJvY3puZS9TcHJhd296ZGFuaWUlMjAyMDI0JTIwJUUyJTgwJTkzJTIwd2Vyc2phJTIwa28lQzUlODRjb3dhLm1kKS4gUG93csOzdDogW3NwaXMgdHJlxZtjaV0oLi4vLi4vaW5kZXgubWQpLgo=",
 "docs/Faktury/FV 2024-03 \u2013 Krak\u00f3w.md": "IyBGViAyMDI0LTAzIOKAkyBLcmFrw7N3CgpGYWt0dXJhIHphIHVzxYJ1Z2kgZG9yYWRjemUuIFpvYmFjeiBbc3ByYXdvemRhbmllXSg8Li4vUmFwb3J0eSByb2N6bmUvU3ByYXdvemRhbmllIDIwMjQg4oCTIHdlcnNqYSBrb8WEY293YS5tZD4pLgo=",
 "docs/Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md": "IyBTcHJhd296ZGFuaWUgcm9jem5lIDIwMjQKClByenljaG9keSB3enJvc8WCeSBvIDEyJSByL3IuIFN6Y3plZ8OzxYJ5IHcgW3phxYLEhWN6bmlrdV0oPHphxYLEhWN6bmlraS93eWtyZXMgc3ByemVkYcW8eS5jc3Y+KS4KClBvcsOzd25haiB6IFtwb3ByemVkbmltIHJva2llbV0oPC4uL0FyY2hpd3VtL0xhdGEgdWJpZWfFgmUvMjAyMyDigJMgcG9kc3Vtb3dhbmllLm1kPikgb3JheiB3csOzxIcgZG8gW3NwaXN1IHRyZcWbY2ldKC4uL2luZGV4Lm1kKS4KCiMjIFN0cnVrdHVyYSBwcnp5Y2hvZMOzdwoKfCBTZWdtZW50IHwgVWR6aWHFgiB8CnwtLS18LS0tfAp8IEh1cnQgfCA2MSUgfAp8IERldGFsIHwgMzklIHwK",
 "docs/index.md": "IyBTdGFydAoKLSBbUmFwb3J0IHJvY3pueV0oUmFwb3J0eSUyMHJvY3puZS9TcHJhd296ZGFuaWUlMjAyMDI0JTIwJUUyJTgwJTkzJTIwd2Vyc2phJTIwa28lQzUlODRjb3dhLm1kKQotIFtQb2RzdW1vd2FuaWUgMjAyM10oPEFyY2hpd3VtL0xhdGEgdWJpZWfFgmUvMjAyMyDigJMgcG9kc3Vtb3dhbmllLm1kPikKLSBbRmFrdHVyYSBtYXJjb3dhXShGYWt0dXJ5L0ZWJTIwMjAyNC0wMyUyMCVFMiU4MCU5MyUyMEtyYWslQzMlQjN3Lm1kKQotIFtQb2xpdHlrYSBwcnl3YXRub8WbY2ldKDxQb2xpdHlrYSBwcnl3YXRub8WbY2kubWQ+KQo=",
 "mkdocs.yml": "c2l0ZV9uYW1lOiBEb2t1bWVudGFjamEgc3DDs8WCa2kKZG9jc19kaXI6IGRvY3MKbmF2OgogIC0gU3RhcnQ6IGluZGV4Lm1kCiAgLSBSYXBvcnR5OgogICAgICAtICJTcHJhd296ZGFuaWUgcm9jem5lIjogIlJhcG9ydHkgcm9jem5lL1NwcmF3b3pkYW5pZSAyMDI0IOKAkyB3ZXJzamEga2/FhGNvd2EubWQiCiAgICAgIC0gIlBvZHN1bW93YW5pZSAyMDIzIjogIkFyY2hpd3VtL0xhdGEgdWJpZWfFgmUvMjAyMyDigJMgcG9kc3Vtb3dhbmllLm1kIgogIC0gRmFrdHVyeTogIkZha3R1cnkvRlYgMjAyNC0wMyDigJMgS3Jha8Ozdy5tZCIKICAtICJQb2xpdHlrYSBwcnl3YXRub8WbY2kiOiAiUG9saXR5a2EgcHJ5d2F0bm/Fm2NpLm1kIgo="
}
DELETE = [
 "docs/Raporty roczne/za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144.md",
 "docs/Raporty roczne/2023 \u2013 podsumowanie.md"
]
ANSWER = "Renamed the 2024 report to `docs/Raporty roczne/Sprawozdanie 2024 \u2013 wersja ko\u0144cowa.md`, moved the 2023 summary to `docs/Archiwum/Lata ubieg\u0142e/2023 \u2013 podsumowanie.md`, and updated every link to them (README.md, docs/index.md, the invoice page, mkdocs.yml nav) plus the relative links inside the two moved files. Percent-encoded links stay percent-encoded and `<...>` links keep their angle brackets."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
