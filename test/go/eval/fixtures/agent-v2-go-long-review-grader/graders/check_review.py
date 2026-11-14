import re, sys
problems = []
want = open("graders/REVIEW.expected.md", encoding="utf-8").read().replace("\r\n", "\n")
got = open("REVIEW.md", encoding="utf-8").read().replace("\r\n", "\n")
if got.rstrip() != want.rstrip():
    problems.append("REVIEW.md: boxes not all ticked or other text changed")
log = open("CHANGELOG.md", encoding="utf-8").read().replace("\r\n", "\n")
m = re.search(r"## Unreleased\n(.*?)(\n## |\Z)", log, re.S)
if not m:
    problems.append("CHANGELOG.md: no Unreleased section")
else:
    lines = [l for l in m.group(1).splitlines() if l.strip()]
    ids = [re.match(r"^- (r\d\d): \S", l).group(1) if re.match(r"^- (r\d\d): \S", l) else "?" for l in lines]
    if ids != ["r%02d" % i for i in range(1, 15)]:
        problems.append("CHANGELOG.md Unreleased entries: %r" % ids)
if "## 1.9.0\n\n- initial import" not in log:
    problems.append("CHANGELOG.md: older sections changed")
for p in problems:
    print(p)
sys.exit(1 if problems else 0)
