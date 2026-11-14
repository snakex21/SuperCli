import re, sys
try:
    text = open("test/pagination.test.ts", encoding="utf-8").read()
except OSError:
    print("test/pagination.test.ts missing"); sys.exit(1)
n = len(re.findall(r"^\s*test\(", text, re.M))
print("agent test cases:", n)
sys.exit(0 if n >= 3 else 1)
