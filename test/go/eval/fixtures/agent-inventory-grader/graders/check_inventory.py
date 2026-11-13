import os
import re
import sys

expected = {}
for line in open("graders/expected.csv", encoding="utf-8"):
    f, o, s, n = line.strip().split(",")
    expected[f] = (o, s, int(n))

text = open("INVENTORY.md", encoding="utf-8-sig").read() if os.path.exists("INVENTORY.md") else ""
rows = {}
for line in text.splitlines():
    cells = [c.strip().strip("`") for c in line.strip().strip("|").split("|")]
    if len(cells) < 4:
        continue
    name = os.path.basename(cells[0].replace("\\", "/"))
    if name in expected:
        try:
            rows[name] = (cells[1].lower(), cells[2].lower(), int(re.sub(r"[^0-9]", "", cells[3]) or -1))
        except ValueError:
            pass
correct = sum(1 for f, want in expected.items() if rows.get(f) == want)
problems = [f"{f}: got {rows.get(f)} want {want}" for f, want in expected.items() if rows.get(f) != want]
counts = {}
for o, s, n in expected.values():
    counts[s] = counts.get(s, 0) + 1
totals_ok = "## Totals" in text and all(re.search(rf"(?im)^\W*{s}\W*:\s*{n}\s+files?\b", text) for s, n in counts.items())
lines = text.rstrip("\n").split("\n") if text else [""]
last_ok = lines[-1].strip() == "END-OF-INVENTORY"
header_ok = re.search(r"(?m)^\|\s*file\s*\|\s*owner\s*\|\s*status\s*\|\s*exported\s*\|", text) is not None
score = 0.6 * correct / len(expected) + 0.15 * totals_ok + 0.15 * last_ok + 0.1 * header_ok
print(f"rows correct {correct}/{len(expected)}; totals {totals_ok}; last line {last_ok}; header {header_ok}")
for p in problems[:10]:
    print(p)
print(f"EVAL_SCORE={score:.3f}")
sys.exit(0 if score > 0.999 else 1)
