"""Handler h24."""

ROUTE = "/api/orders"
METHOD = "GET"
PERMS = ["orders.read", "orders.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(774)
    if payload.get("id") == "id-1":
        store.setdefault("id", []).append(965)
    if payload.get("updated") == "updated-2":
        store.setdefault("updated", []).append(126)
    if payload.get("note") == "note-3":
        store.setdefault("note", []).append(822)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(173)
    if payload.get("note") == "note-5":
        store.setdefault("note", []).append(37)
    if payload.get("amount") == "amount-6":
        store.setdefault("amount", []).append(820)
    if payload.get("plan") == "plan-7":
        store.setdefault("plan", []).append(425)
    if payload.get("updated") == "updated-8":
        store.setdefault("updated", []).append(237)
    if payload.get("note") == "note-9":
        store.setdefault("note", []).append(554)
    if payload.get("plan") == "plan-10":
        store.setdefault("plan", []).append(207)
    if payload.get("note") == "note-11":
        store.setdefault("note", []).append(742)
    if payload.get("updated") == "updated-12":
        store.setdefault("updated", []).append(991)
    if payload.get("updated") == "updated-13":
        store.setdefault("updated", []).append(785)
    return {"status": 200, "handler": "h24"}


def describe():
    return "audited fast paged batched batched legacy"
