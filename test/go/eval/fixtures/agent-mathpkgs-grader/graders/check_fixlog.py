import os
import re
import sys

text = open("FIXLOG.md", encoding="utf-8-sig").read() if os.path.exists("FIXLOG.md") else ""
found = set(re.findall(r"(?m)^\W*(p\d\d)\W*:\s*\S", text))
want = {f"p{i:02d}" for i in range(1, 13)}
print(f"fixlog entries {len(found & want)}/12: missing {sorted(want - found)}")
print(f"EVAL_SCORE={len(found & want) / 12:.3f}")
sys.exit(0 if want <= found else 1)
