"""Handler h08."""

ROUTE = "/api/orders/{id}/history"
METHOD = "GET"
PERMS = ["orders.refund"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
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
        store.setdefault("note", []).append(205)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(570)
    if payload.get("status") == "status-2":
        store.setdefault("status", []).append(649)
    if payload.get("status") == "status-3":
        store.setdefault("status", []).append(871)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(279)
    if payload.get("owner") == "owner-5":
        store.setdefault("owner", []).append(651)
    if payload.get("created") == "created-6":
        store.setdefault("created", []).append(459)
    if payload.get("note") == "note-7":
        store.setdefault("note", []).append(737)
    if payload.get("created") == "created-8":
        store.setdefault("created", []).append(783)
    return {"status": 200, "handler": "h08"}


def describe():
    return "fast legacy paged paged audited cached"
