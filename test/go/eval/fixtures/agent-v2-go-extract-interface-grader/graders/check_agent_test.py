import glob, os, re, sys
files = [f for f in glob.glob("internal/service/*_test.go") if not os.path.basename(f).startswith("zz_hidden")]
ok = False
for f in files:
    text = open(f, encoding="utf-8", errors="replace").read()
    if re.search(r"func Test\w+\(t \*testing\.T\)", text) and re.search(r"\)\s*Send\(", text):
        ok = True
if not ok:
    print("no agent-written test with a fake Send implementation in internal/service")
    sys.exit(1)
src = [f for f in glob.glob("internal/service/*.go") if not f.endswith("_test.go")]
if not any(re.search(r"var\s+_\s+Sender\s*=\s*(\(\*smtp\.Client\)\(nil\)|&smtp\.Client\{\}|new\(smtp\.Client\))", open(f, encoding="utf-8").read()) for f in src):
    print("no compile-time assertion `var _ Sender = (*smtp.Client)(nil)` in package service")
    sys.exit(1)
print("ok")
