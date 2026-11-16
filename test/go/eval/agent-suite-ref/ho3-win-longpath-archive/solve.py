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
 "Archiwum um\u00f3w/Mi\u0119dzynarodowe Towarzystwo Ubezpiecze\u0144 Wzajemnych Rolnik\u00f3w i Sadownik\u00f3w Po\u0142udniowej Ma\u0142opolski/2023/AN-2023-0042 \u2013 aneks nr 3 do umowy o \u015bwiadczenie us\u0142ug ubezpieczeniowych floty pojazd\u00f3w.md": "LS0tDQpudW1lcjogQU4tMjAyMy0wMDQyDQprbGllbnQ6IEstMDQyDQpyb2s6IDIwMjMNCnR5dHXFgjogYW5la3MgbnIgMyBkbyB1bW93eSBvIMWbd2lhZGN6ZW5pZSB1c8WCdWcgdWJlenBpZWN6ZW5pb3d5Y2ggZmxvdHkgcG9qYXpkw7N3DQotLS0NCg0KIyBBbmVrcyBuciAzIGRvIHVtb3d5IG8gxZt3aWFkY3plbmllIHVzxYJ1ZyB1YmV6cGllY3plbmlvd3ljaCBmbG90eSBwb2phemTDs3cNCg0KU3Ryb255IHVzdGFsYWrEhSwgY28gbmFzdMSZcHVqZTogwqcgMS4gUHJ6ZWRtaW90IHVtb3d5IG9waXNhbm8gdyB6YcWCxIVjem5pa3UgbnIgMS4NCsKnIDIuIFd5bmFncm9kemVuaWUgcMWCYXRuZSB3IHRlcm1pbmllIDE0IGRuaSBvZCBkb3LEmWN6ZW5pYSBmYWt0dXJ5Lg0KDQpQb2RwaXNhbm8ga3dhbGlmaWtvd2FueW0gcG9kcGlzZW0gZWxla3Ryb25pY3pueW0uDQo=",
 "Archiwum um\u00f3w/Przedsi\u0119biorstwo Produkcyjno-Handlowo-Us\u0142ugowe Zielony Gaj Sp\u00f3\u0142ka z ograniczon\u0105 odpowiedzialno\u015bci\u0105 sp\u00f3\u0142ka komandytowa/2024/UM-2024-0173 \u2013 umowa ramowa na dostawy materia\u0142\u00f3w biurowych i eksploatacyjnych.md": "LS0tDQpudW1lcjogVU0tMjAyNC0wMTczDQprbGllbnQ6IEstMDE3DQpyb2s6IDIwMjQNCnR5dHXFgjogdW1vd2EgcmFtb3dhIG5hIGRvc3Rhd3kgbWF0ZXJpYcWCw7N3IGJpdXJvd3ljaCBpIGVrc3Bsb2F0YWN5am55Y2gNCi0tLQ0KDQojIFVtb3dhIHJhbW93YSBuYSBkb3N0YXd5IG1hdGVyaWHFgsOzdyBiaXVyb3d5Y2ggaSBla3NwbG9hdGFjeWpueWNoDQoNClN0cm9ueSB1c3RhbGFqxIUsIGNvIG5hc3TEmXB1amU6IMKnIDEuIFByemVkbWlvdCB1bW93eSBvcGlzYW5vIHcgemHFgsSFY3puaWt1IG5yIDEuDQrCpyAyLiBXeW5hZ3JvZHplbmllIHDFgmF0bmUgdyB0ZXJtaW5pZSAxNCBkbmkgb2QgZG9yxJljemVuaWEgZmFrdHVyeS4NCg0KUG9kcGlzYW5vIGt3YWxpZmlrb3dhbnltIHBvZHBpc2VtIGVsZWt0cm9uaWN6bnltLg0K",
 "Archiwum um\u00f3w/Sp\u00f3\u0142dzielnia Mieszkaniowa Lokatorsko-W\u0142asno\u015bciowa Przysz\u0142o\u015b\u0107 im. J\u00f3zefa Wybickiego w Gda\u0144sku-Wrzeszczu/2025/UM-2025-0007 \u2013 umowa o administrowanie nieruchomo\u015bciami wsp\u00f3lnymi osiedla Przysz\u0142o\u015b\u0107.md": "LS0tDQpudW1lcjogVU0tMjAyNS0wMDA3DQprbGllbnQ6IEstMTA1DQpyb2s6IDIwMjUNCnR5dHXFgjogdW1vd2EgbyBhZG1pbmlzdHJvd2FuaWUgbmllcnVjaG9tb8WbY2lhbWkgd3Nww7NsbnltaSBvc2llZGxhIFByenlzesWCb8WbxIcNCi0tLQ0KDQojIFVtb3dhIG8gYWRtaW5pc3Ryb3dhbmllIG5pZXJ1Y2hvbW/Fm2NpYW1pIHdzcMOzbG55bWkgb3NpZWRsYSBwcnp5c3rFgm/Fm8SHDQoNClN0cm9ueSB1c3RhbGFqxIUsIGNvIG5hc3TEmXB1amU6IMKnIDEuIFByemVkbWlvdCB1bW93eSBvcGlzYW5vIHcgemHFgsSFY3puaWt1IG5yIDEuDQrCpyAyLiBXeW5hZ3JvZHplbmllIHDFgmF0bmUgdyB0ZXJtaW5pZSAxNCBkbmkgb2QgZG9yxJljemVuaWEgZmFrdHVyeS4NCg0KUG9kcGlzYW5vIGt3YWxpZmlrb3dhbnltIHBvZHBpc2VtIGVsZWt0cm9uaWN6bnltLg0K"
}
DELETE = [
 "do_archiwizacji/AN-2023-0042.md",
 "do_archiwizacji/UM-2024-0173.md",
 "do_archiwizacji/UM-2025-0007.md"
]
ANSWER = "Archived the three documents under `Archiwum um\u00f3w/<client>/<year>/<number> \u2013 <title>.md` (byte-identical moves; the longest path is 218 characters relative to the project) and emptied `do_archiwizacji/`."

for rel in DELETE:
    os.remove(lp(rel))
for rel, b in sorted(FILES.items()):
    wb(lp(rel), base64.b64decode(b))
sys.stdout.write(ANSWER + "\n")
