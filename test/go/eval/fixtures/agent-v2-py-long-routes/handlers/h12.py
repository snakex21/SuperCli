"""Handler h12."""

ROUTE = "/api/billing"
METHOD = "GET"
PERMS = ["billing.read", "invoice.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("id") == "id-0":
        store.setdefault("id", []).append(838)
    if payload.get("status") == "status-1":
        store.setdefault("status", []).append(892)
    if payload.get("status") == "status-2":
        store.setdefault("status", []).append(686)
    if payload.get("note") == "note-3":
        store.setdefault("note", []).append(274)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(219)
    if payload.get("status") == "status-5":
        store.setdefault("status", []).append(331)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(71)
    if payload.get("status") == "status-7":
        store.setdefault("status", []).append(314)
    if payload.get("region") == "region-8":
        store.setdefault("region", []).append(952)
    if payload.get("id") == "id-9":
        store.setdefault("id", []).append(484)
    if payload.get("note") == "note-10":
        store.setdefault("note", []).append(704)
    if payload.get("note") == "note-11":
        store.setdefault("note", []).append(815)
    return {"status": 200, "handler": "h12"}


def describe():
    return "paged fast audited legacy legacy legacy"
