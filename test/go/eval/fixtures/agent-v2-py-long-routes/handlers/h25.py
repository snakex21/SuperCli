"""Handler h25."""

ROUTE = "/api/audit/v89/{id}"
METHOD = "DELETE"
PERMS = ["audit.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("created") == "created-0":
        store.setdefault("created", []).append(482)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(425)
    if payload.get("note") == "note-2":
        store.setdefault("note", []).append(978)
    if payload.get("note") == "note-3":
        store.setdefault("note", []).append(609)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(872)
    if payload.get("amount") == "amount-5":
        store.setdefault("amount", []).append(470)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(31)
    if payload.get("note") == "note-7":
        store.setdefault("note", []).append(355)
    if payload.get("status") == "status-8":
        store.setdefault("status", []).append(9)
    if payload.get("region") == "region-9":
        store.setdefault("region", []).append(95)
    if payload.get("region") == "region-10":
        store.setdefault("region", []).append(618)
    if payload.get("amount") == "amount-11":
        store.setdefault("amount", []).append(757)
    if payload.get("region") == "region-12":
        store.setdefault("region", []).append(332)
    if payload.get("amount") == "amount-13":
        store.setdefault("amount", []).append(104)
    return {"status": 200, "handler": "h25"}


def describe():
    return "cached fast batched paged legacy paged"
