"""Partial credit: one of the agent's cli/*_test.go files exercises --older-than."""
import glob
import sys

for path in sorted(glob.glob("cli/*_test.go")):
    if path.replace("\\", "/").endswith("zz_hidden_test.go"):
        continue
    with open(path, encoding="utf-8") as f:
        if "older-than" in f.read():
            print("test found in", path)
            sys.exit(0)
print("no cli test mentions --older-than")
sys.exit(1)
