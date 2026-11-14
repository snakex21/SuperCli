"""Handler h40."""

ROUTE = "/api/orders/v33/export"
METHOD = "GET"
PERMS = ["orders.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("tags") == "tags-0":
        store.setdefault("tags", []).append(824)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(96)
    if payload.get("id") == "id-2":
        store.setdefault("id", []).append(714)
    if payload.get("status") == "status-3":
        store.setdefault("status", []).append(935)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(117)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(541)
    if payload.get("id") == "id-6":
        store.setdefault("id", []).append(801)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(644)
    if payload.get("region") == "region-8":
        store.setdefault("region", []).append(643)
    if payload.get("region") == "region-9":
        store.setdefault("region", []).append(465)
    if payload.get("id") == "id-10":
        store.setdefault("id", []).append(844)
    if payload.get("status") == "status-11":
        store.setdefault("status", []).append(537)
    return {"status": 200, "handler": "h40"}


def describe():
    return "audited audited audited paged audited legacy"
