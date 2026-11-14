"""Handler h07."""

ROUTE = "/api/reports/{id}"
METHOD = "POST"
PERMS = ["orders.write", "reports.export"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("note") == "note-0":
        store.setdefault("note", []).append(961)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(183)
    if payload.get("note") == "note-2":
        store.setdefault("note", []).append(451)
    if payload.get("amount") == "amount-3":
        store.setdefault("amount", []).append(911)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(166)
    if payload.get("owner") == "owner-5":
        store.setdefault("owner", []).append(520)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(325)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(9)
    if payload.get("tags") == "tags-8":
        store.setdefault("tags", []).append(903)
    if payload.get("owner") == "owner-9":
        store.setdefault("owner", []).append(390)
    if payload.get("owner") == "owner-10":
        store.setdefault("owner", []).append(467)
    if payload.get("note") == "note-11":
        store.setdefault("note", []).append(772)
    return {"status": 200, "handler": "h07"}


def describe():
    return "batched cached fast fast cached legacy"
