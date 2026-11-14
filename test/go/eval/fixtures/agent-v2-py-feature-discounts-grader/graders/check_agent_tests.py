import re, sys
try:
    text = open("tests/test_discounts.py", encoding="utf-8").read()
except OSError:
    print("tests/test_discounts.py missing"); sys.exit(1)
n = len(re.findall(r"^\s+def test_\w+\(self", text, re.M))
print("agent test methods:", n)
sys.exit(0 if n >= 4 else 1)
