"""Mixed-language repository (Go producer, TypeScript web client, Python
analytics) sharing one event schema; add a field end to end."""
import json

from common import GRADER_PRELUDE, Task, cmd, d, sub

SCHEMA = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "title": "OrderEvent",
    "type": "object",
    "additionalProperties": False,
    "required": ["id", "sku", "quantity", "price_cents"],
    "properties": {
        "id": {"type": "string", "minLength": 1},
        "sku": {"type": "string", "pattern": "^[A-Z]{3}-[0-9]{4}$"},
        "quantity": {"type": "integer", "minimum": 1},
        "price_cents": {"type": "integer", "minimum": 0},
    },
}

EVENT_GO = d('''
    // Package event converts internal orders into the OrderEvent wire format
    // described by schema/order_event.schema.json (consumed by web/ and analytics/).
    package event

    import (
    	"encoding/json"
    	"fmt"
    )

    // Order is an order row as stored by the order service.
    type Order struct {
    	ID         string
    	SKU        string
    	Quantity   int
    	PriceCents int64
    	// Currency is the ISO 4217 code; rows created before 2025 have it empty
    	// (those orders were all in PLN).
    	Currency string
    }

    // OrderEvent is the published JSON event.
    type OrderEvent struct {
    	ID         string `json:"id"`
    	SKU        string `json:"sku"`
    	Quantity   int    `json:"quantity"`
    	PriceCents int64  `json:"price_cents"`
    }

    // FromOrder builds the event for an order.
    func FromOrder(o Order) (OrderEvent, error) {
    	if o.Quantity < 1 {
    		return OrderEvent{}, fmt.Errorf("order %s: quantity must be positive", o.ID)
    	}
    	return OrderEvent{ID: o.ID, SKU: o.SKU, Quantity: o.Quantity, PriceCents: o.PriceCents}, nil
    }

    // Encode renders one event as a JSON line.
    func Encode(e OrderEvent) ([]byte, error) {
    	b, err := json.Marshal(e)
    	if err != nil {
    		return nil, err
    	}
    	return append(b, '\\n'), nil
    }
    ''')

EVENT_GO_SOLVED = sub(
    EVENT_GO,
    ("\tPriceCents int64  `json:\"price_cents\"`\n}",
     "\tPriceCents int64  `json:\"price_cents\"`\n\tCurrency   string `json:\"currency\"`\n}"),
    ("\treturn OrderEvent{ID: o.ID, SKU: o.SKU, Quantity: o.Quantity, PriceCents: o.PriceCents}, nil\n",
     "\tcurrency := o.Currency\n"
     "\tif currency == \"\" {\n"
     "\t\tcurrency = \"PLN\" // legacy rows\n"
     "\t}\n"
     "\treturn OrderEvent{ID: o.ID, SKU: o.SKU, Quantity: o.Quantity, PriceCents: o.PriceCents, Currency: currency}, nil\n"),
)

EVENT_TEST_GO = d('''
    package event

    import (
    	"strings"
    	"testing"
    )

    func TestFromOrder(t *testing.T) {
    	e, err := FromOrder(Order{ID: "o-1", SKU: "ABC-0001", Quantity: 2, PriceCents: 1999})
    	if err != nil {
    		t.Fatal(err)
    	}
    	b, err := Encode(e)
    	if err != nil {
    		t.Fatal(err)
    	}
    	if !strings.Contains(string(b), `"price_cents":1999`) {
    		t.Fatalf("encoded %s", b)
    	}
    }

    func TestRejectsZeroQuantity(t *testing.T) {
    	if _, err := FromOrder(Order{ID: "o-2", Quantity: 0}); err == nil {
    		t.Fatal("expected error")
    	}
    }
    ''')

EMIT_GO = d('''
    // Command emit reads a JSON array of orders and prints one OrderEvent JSON
    // line per order (used by contract/check_contract.py).
    package main

    import (
    	"encoding/json"
    	"fmt"
    	"os"

    	"polyglot/producer/event"
    )

    func main() {
    	if len(os.Args) != 2 {
    		fmt.Fprintln(os.Stderr, "usage: emit ORDERS.json")
    		os.Exit(2)
    	}
    	data, err := os.ReadFile(os.Args[1])
    	if err != nil {
    		fmt.Fprintln(os.Stderr, err)
    		os.Exit(1)
    	}
    	var orders []event.Order
    	if err := json.Unmarshal(data, &orders); err != nil {
    		fmt.Fprintln(os.Stderr, err)
    		os.Exit(1)
    	}
    	for _, o := range orders {
    		e, err := event.FromOrder(o)
    		if err != nil {
    			fmt.Fprintln(os.Stderr, err)
    			os.Exit(1)
    		}
    		b, _ := event.Encode(e)
    		os.Stdout.Write(b)
    	}
    }
    ''')

ORDER_TS = d('''
    // Wire format of order events (schema/order_event.schema.json).
    export interface OrderEvent {
      id: string;
      sku: string;
      quantity: number;
      price_cents: number;
    }

    function field(o: Record<string, unknown>, name: string): unknown {
      if (!(name in o)) {
        throw new Error(`order event: missing ${name}`);
      }
      return o[name];
    }

    function str(o: Record<string, unknown>, name: string, pattern?: RegExp): string {
      const v = field(o, name);
      if (typeof v !== "string" || v.length === 0 || (pattern && !pattern.test(v))) {
        throw new Error(`order event: invalid ${name}`);
      }
      return v;
    }

    function int(o: Record<string, unknown>, name: string, min: number): number {
      const v = field(o, name);
      if (typeof v !== "number" || !Number.isInteger(v) || v < min) {
        throw new Error(`order event: invalid ${name}`);
      }
      return v;
    }

    /** Validates an untrusted decoded JSON value and returns a typed event. */
    export function parseOrderEvent(input: unknown): OrderEvent {
      if (typeof input !== "object" || input === null || Array.isArray(input)) {
        throw new Error("order event: not an object");
      }
      const o = input as Record<string, unknown>;
      return {
        id: str(o, "id"),
        sku: str(o, "sku", /^[A-Z]{3}-[0-9]{4}$/),
        quantity: int(o, "quantity", 1),
        price_cents: int(o, "price_cents", 0),
      };
    }
    ''')

ORDER_TS_SOLVED = sub(
    ORDER_TS,
    ("  price_cents: number;\n}", "  price_cents: number;\n  currency: string;\n}"),
    ('    price_cents: int(o, "price_cents", 0),\n', '    price_cents: int(o, "price_cents", 0),\n    currency: str(o, "currency", /^[A-Z]{3}$/),\n'),
)

ORDER_TEST_TS = d('''
    import test from "node:test";
    import assert from "node:assert/strict";
    import { parseOrderEvent } from "../src/orderEvent.ts";

    const valid = { id: "o-1", sku: "ABC-0001", quantity: 2, price_cents: 1999, currency: "PLN" };

    test("parses a valid event", () => {
      const e = parseOrderEvent(valid);
      assert.equal(e.sku, "ABC-0001");
      assert.equal(e.price_cents, 1999);
    });

    test("rejects a bad sku", () => {
      assert.throws(() => parseOrderEvent({ ...valid, sku: "abc-1" }));
    });

    test("rejects a non-object", () => {
      assert.throws(() => parseOrderEvent([valid]));
    });
    ''')

VALIDATE_TS = d('''
    // Reads JSON lines on stdin, validates each with parseOrderEvent and prints
    // the parsed event as canonical JSON (keys sorted). Used by the contract check.
    import { parseOrderEvent } from "../src/orderEvent.ts";

    let buf = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", (c) => (buf += c));
    process.stdin.on("end", () => {
      for (const line of buf.split("\\n")) {
        if (!line.trim()) continue;
        const e = parseOrderEvent(JSON.parse(line)) as unknown as Record<string, unknown>;
        const sorted = Object.fromEntries(Object.keys(e).sort().map((k) => [k, e[k]]));
        process.stdout.write(JSON.stringify(sorted) + "\\n");
      }
    });
    ''')

EVENTS_PY = d('''
    """Order events as consumed by the analytics jobs (schema/order_event.schema.json)."""
    import re
    from dataclasses import dataclass

    _SKU = re.compile(r"^[A-Z]{3}-[0-9]{4}$")


    @dataclass(frozen=True)
    class OrderEvent:
        id: str
        sku: str
        quantity: int
        price_cents: int


    def _int(d, name, minimum):
        v = d.get(name)
        if not isinstance(v, int) or isinstance(v, bool) or v < minimum:
            raise ValueError("order event: invalid %s" % name)
        return v


    def load_event(d):
        """Validate a decoded JSON dict (live bus or the archive) and build an OrderEvent."""
        if not isinstance(d, dict):
            raise ValueError("order event: not an object")
        if not isinstance(d.get("id"), str) or not d["id"]:
            raise ValueError("order event: invalid id")
        if not isinstance(d.get("sku"), str) or not _SKU.match(d["sku"]):
            raise ValueError("order event: invalid sku")
        return OrderEvent(id=d["id"], sku=d["sku"], quantity=_int(d, "quantity", 1),
                          price_cents=_int(d, "price_cents", 0))
    ''')

EVENTS_PY_SOLVED = sub(
    EVENTS_PY,
    ('_SKU = re.compile(r"^[A-Z]{3}-[0-9]{4}$")\n', '_SKU = re.compile(r"^[A-Z]{3}-[0-9]{4}$")\n_CURRENCY = re.compile(r"^[A-Z]{3}$")\n'),
    ("    price_cents: int\n", "    price_cents: int\n    currency: str = \"PLN\"\n"),
    ('''        raise ValueError("order event: invalid sku")
    return OrderEvent(id=d["id"], sku=d["sku"], quantity=_int(d, "quantity", 1),
                      price_cents=_int(d, "price_cents", 0))
''', '''        raise ValueError("order event: invalid sku")
    currency = d.get("currency", "PLN")  # archived events predate the field
    if not isinstance(currency, str) or not _CURRENCY.match(currency):
        raise ValueError("order event: invalid currency")
    return OrderEvent(id=d["id"], sku=d["sku"], quantity=_int(d, "quantity", 1),
                      price_cents=_int(d, "price_cents", 0), currency=currency)
'''),
)

EVENTS_TEST_PY = d('''
    import unittest

    from analytics.events import load_event


    class LoadEventTest(unittest.TestCase):
        def test_valid(self):
            e = load_event({"id": "o-1", "sku": "ABC-0001", "quantity": 2, "price_cents": 1999, "currency": "PLN"})
            self.assertEqual(e.quantity, 2)

        def test_rejects_bool_quantity(self):
            with self.assertRaises(ValueError):
                load_event({"id": "o-1", "sku": "ABC-0001", "quantity": True, "price_cents": 1})


    if __name__ == "__main__":
        unittest.main()
    ''')

CONTRACT_PY = d(r'''
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
    ''')

SAMPLE_ORDERS = [
    {"ID": "o-100", "SKU": "KAW-0001", "Quantity": 3, "PriceCents": 2599, "Currency": "PLN"},
    {"ID": "o-101", "SKU": "HER-0420", "Quantity": 1, "PriceCents": 899, "Currency": "EUR"},
    {"ID": "o-102", "SKU": "CUK-0007", "Quantity": 12, "PriceCents": 150, "Currency": ""},
]


def build():
    t = Task("polyglot-add-field")
    t.fx("README.md", d('''
        # polyglot orders

        - `producer/` (Go): turns orders into OrderEvent JSON lines - `cd producer && go test ./...`
        - `web/` (TypeScript, Node 24 runs .ts directly): `parseOrderEvent` - `node --test web/test/orderEvent.test.ts`
        - `analytics/` (Python): `load_event` - `python -m unittest discover -s analytics/tests -t .`
        - `schema/order_event.schema.json`: the contract; `python contract/check_contract.py` checks all three agree.
        '''))
    t.fx("schema/order_event.schema.json", json.dumps(SCHEMA, indent=2) + "\n")
    t.fx("producer/go.mod", "module polyglot/producer\n\ngo 1.22\n")
    t.fx("producer/event/event.go", EVENT_GO)
    t.fx("producer/event/event_test.go", EVENT_TEST_GO)
    t.fx("producer/cmd/emit/main.go", EMIT_GO)
    t.fx("web/package.json", json.dumps({"name": "orders-web", "private": True, "type": "module",
                                         "scripts": {"test": "node --test test/orderEvent.test.ts"}}, indent=2) + "\n")
    t.fx("web/src/orderEvent.ts", ORDER_TS)
    t.fx("web/test/orderEvent.test.ts", ORDER_TEST_TS)
    t.fx("web/scripts/validate.ts", VALIDATE_TS)
    t.fx("analytics/__init__.py", "")
    t.fx("analytics/events.py", EVENTS_PY)
    t.fx("analytics/tests/__init__.py", "")
    t.fx("analytics/tests/test_events.py", EVENTS_TEST_PY)
    t.fx("contract/check_contract.py", CONTRACT_PY)
    t.fx("contract/sample_orders.json", json.dumps(SAMPLE_ORDERS, indent=2) + "\n")

    t.gr("producer/event/zz_hidden_test.go", d('''
        package event

        import (
        	"strings"
        	"testing"
        )

        func TestHiddenCurrency(t *testing.T) {
        	e, err := FromOrder(Order{ID: "o-9", SKU: "ABC-0001", Quantity: 1, PriceCents: 5, Currency: "EUR"})
        	if err != nil {
        		t.Fatal(err)
        	}
        	if e.Currency != "EUR" {
        		t.Fatalf("Currency = %q", e.Currency)
        	}
        	b, _ := Encode(e)
        	if !strings.Contains(string(b), `"currency":"EUR"`) {
        		t.Fatalf("encoded %s", b)
        	}
        	legacy, err := FromOrder(Order{ID: "o-8", SKU: "ABC-0001", Quantity: 1, PriceCents: 5})
        	if err != nil || legacy.Currency != "PLN" {
        		t.Fatalf("legacy order: %+v %v", legacy, err)
        	}
        }
        '''))
    t.gr("web/test/zz_hidden.test.ts", d('''
        import test from "node:test";
        import assert from "node:assert/strict";
        import { parseOrderEvent } from "../src/orderEvent.ts";

        const base = { id: "o-1", sku: "ABC-0001", quantity: 2, price_cents: 1999 };

        test("hidden: currency is parsed", () => {
          assert.equal((parseOrderEvent({ ...base, currency: "EUR" }) as any).currency, "EUR");
        });

        test("hidden: missing or malformed currency is rejected", () => {
          assert.throws(() => parseOrderEvent({ ...base }));
          for (const c of ["eur", "EURO", "EU", "", 978, null]) {
            assert.throws(() => parseOrderEvent({ ...base, currency: c }), String(c));
          }
        });
        '''))
    argv = t.grade_script(GRADER_PRELUDE + d(r'''
        import json
        import re

        sys.path.insert(0, os.getcwd())
        from analytics.events import load_event  # noqa: E402

        base = {"id": "o-1", "sku": "ABC-0001", "quantity": 2, "price_cents": 1999}
        try:
            e = load_event(dict(base))
            check("archived event without currency -> PLN", getattr(e, "currency", None) == "PLN", e)
        except Exception as ex:  # noqa: BLE001
            check("archived event without currency -> PLN", False, ex)
        try:
            check("currency kept", load_event(dict(base, currency="USD")).currency == "USD")
        except Exception as ex:  # noqa: BLE001
            check("currency kept", False, ex)
        for bad in ("usd", "USDX", "", 840, None):
            try:
                load_event(dict(base, currency=bad))
                check("rejects currency %r" % (bad,), False, "accepted")
            except ValueError:
                check("rejects currency %r" % (bad,), True)
            except Exception as ex:  # noqa: BLE001
                check("rejects currency %r" % (bad,), False, "raised %r, want ValueError" % ex)

        with open("schema/order_event.schema.json", encoding="utf-8") as f:
            schema = json.load(f)
        prop = schema.get("properties", {}).get("currency")
        check("schema: currency property", isinstance(prop, dict), prop)
        check("schema: currency required", "currency" in schema.get("required", []))
        if isinstance(prop, dict):
            check("schema: currency is a string", prop.get("type") == "string", prop)
            pat, enum = prop.get("pattern"), prop.get("enum")
            if pat:
                ok = all(bool(re.search(pat, v)) for v in ("PLN", "EUR")) and not any(re.search(pat, v) for v in ("pln", "EURO", "EU"))
                check("schema: pattern accepts only 3 uppercase letters (JSON Schema search semantics)", ok, pat)
            else:
                check("schema: pattern or enum restricts the code", bool(enum) and "PLN" in enum and "EUR" in enum, prop)
        finish()
        '''))
    t.solve_files({
        "producer/event/event.go": EVENT_GO_SOLVED,
        "web/src/orderEvent.ts": ORDER_TS_SOLVED,
        "analytics/events.py": EVENTS_PY_SOLVED,
        "schema/order_event.schema.json": json.dumps(dict(
            SCHEMA, required=SCHEMA["required"] + ["currency"],
            properties=dict(SCHEMA["properties"], currency={"type": "string", "pattern": "^[A-Z]{3}$"})), indent=2) + "\n",
    }, answer=("Added `currency` end to end: schema (required, `^[A-Z]{3}$`), Go `OrderEvent.Currency` "
               "(`json:\"currency\"`, empty legacy currency published as PLN), TypeScript `OrderEvent.currency` with "
               "validation in `parseOrderEvent`, and Python `OrderEvent.currency` where archived events without the "
               "key default to PLN and malformed codes raise ValueError. Go, Node and Python tests and the contract "
               "check pass."))
    t.register(
        name="Add a field to a shared event schema across Go, TypeScript and Python",
        description=("Cross-language change: JSON schema, Go producer struct/defaulting, TypeScript type + "
                     "validator, Python dataclass + loader with an archive-compatibility rule. Graded by hidden "
                     "tests in all three languages plus the repo's own contract check (producer -> TS -> Python)."),
        category="coding",
        tags=["polyglot", "go", "typescript", "python", "schema", "cross-language"],
        difficulty="hard",
        prompt=("Add a `currency` field to the order event, end to end. It is a 3-letter uppercase ISO 4217 code (`PLN`, `EUR`, ...).\n"
                "- `schema/order_event.schema.json`: required string, restricted to three uppercase letters.\n"
                "- Go producer: `OrderEvent.Currency` with JSON name `currency`, copied from `Order.Currency`; legacy orders "
                "with an empty currency are published as `PLN`.\n"
                "- TypeScript (`web/`): add it to `OrderEvent`; `parseOrderEvent` must reject a missing or malformed currency.\n"
                "- Python (`analytics/`): add `currency` to the `OrderEvent` dataclass. `load_event` must still accept archived "
                "events that predate the field (no `currency` key) and treat them as `PLN`; a present but malformed currency "
                "raises `ValueError`.\n"
                "The existing Go, Node and Python tests and `python contract/check_contract.py` must pass. Don't change the "
                "contract script or the sample data."),
        expectedChangedFiles=["schema/order_event.schema.json", "producer/event/event.go", "web/src/orderEvent.ts",
                              "analytics/events.py"],
        forbiddenChangedFiles=["contract/**", "producer/go.mod"],
        strictDiff=True,
        allowedChangedFiles=["schema/**", "producer/**", "web/**", "analytics/**", "README.md"],
        verificationCommands=[
            cmd("go-test", "go", "-C", "producer", "test", "-count=1", "./..."),
            cmd("node-test", "node", "--test", "web/test/orderEvent.test.ts", "web/test/zz_hidden.test.ts"),
            cmd("python-test", "python", "-m", "unittest", "discover", "-s", "analytics/tests", "-t", "."),
            cmd("contract", "python", "contract/check_contract.py"),
            cmd("python-and-schema", *argv),
        ],
    )
