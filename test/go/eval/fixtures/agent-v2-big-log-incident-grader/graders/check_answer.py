import json, sys
want = json.load(open("graders/expected.json", encoding="utf-8"))
try:
    got = json.load(open("ANSWER.json", encoding="utf-8-sig"))
except Exception as e:
    print("ANSWER.json unreadable:", e); sys.exit(1)
bad = []
def get(d, *ks):
    for k in ks:
        if not isinstance(d, dict) or k not in d:
            return None
        d = d[k]
    return d
for path in (("first_5xx_after_deploy", "request_id"), ("first_5xx_after_deploy", "upstream"), ("distinct_users_503",), ("top_failing_endpoint",), ("top_failing_count",), ("search_p95_ms",)):
    if get(got, *path) != get(want, *path):
        bad.append("%s: got %r" % (".".join(path), get(got, *path)))
for b in bad:
    print(b)
if bad:
    print("EVAL_SCORE=%.2f" % (1 - len(bad) / 6.0))
sys.exit(1 if bad else 0)
