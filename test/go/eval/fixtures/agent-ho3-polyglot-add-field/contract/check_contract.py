"""Cross-language contract check: the Go producer, the TypeScript parser, the
Python loader and the JSON schema must agree on the event fields.

    python contract/check_contract.py
"""
import dataclasses
import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)
from analytics.events import OrderEvent, load_event  # noqa: E402


def main():
    with open(os.path.join(ROOT, "schema", "order_event.schema.json"), encoding="utf-8") as f:
        schema = json.load(f)
    emitted = subprocess.run(["go", "run", "./cmd/emit", os.path.join(ROOT, "contract", "sample_orders.json")],
                             cwd=os.path.join(ROOT, "producer"), capture_output=True, text=True)
    if emitted.returncode != 0:
        sys.exit("emit failed: " + emitted.stderr)
    events = [json.loads(l) for l in emitted.stdout.splitlines() if l.strip()]
    parsed = subprocess.run(["node", os.path.join(ROOT, "web", "scripts", "validate.ts")], input=emitted.stdout,
                            capture_output=True, text=True)
    if parsed.returncode != 0:
        sys.exit("TypeScript parser rejected the producer output: " + parsed.stderr)
    ts_events = [json.loads(l) for l in parsed.stdout.splitlines() if l.strip()]
    py_events = [dataclasses.asdict(load_event(e)) for e in events]

    go_keys = set().union(*(e.keys() for e in events))
    sets = {
        "schema.required": set(schema["required"]),
        "schema.properties": set(schema["properties"]),
        "go producer": go_keys,
        "typescript parser": set().union(*(e.keys() for e in ts_events)),
        "python dataclass": {f.name for f in dataclasses.fields(OrderEvent)},
    }
    ref = sets["schema.properties"]
    bad = {k: sorted(v ^ ref) for k, v in sets.items() if v != ref}
    if bad:
        sys.exit("field sets disagree with schema.properties: %s" % bad)
    for e, t, p in zip(events, ts_events, py_events):
        if not (e == t == p):
            sys.exit("round trip differs:\n go %s\n ts %s\n py %s" % (e, t, p))
    print("contract ok: %d events, fields %s" % (len(events), sorted(ref)))


if __name__ == "__main__":
    main()
