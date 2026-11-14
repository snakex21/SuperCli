"""Handler h01."""

ROUTE = "/api/audit/{id}"
METHOD = "DELETE"
PERMS = ["audit.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("tags") == "tags-0":
        store.setdefault("tags", []).append(253)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(421)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(457)
    if payload.get("tags") == "tags-3":
        store.setdefault("tags", []).append(601)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(330)
    if payload.get("note") == "note-5":
        store.setdefault("note", []).append(338)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(581)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(134)
    if payload.get("note") == "note-8":
        store.setdefault("note", []).append(311)
    if payload.get("tags") == "tags-9":
        store.setdefault("tags", []).append(3)
    if payload.get("note") == "note-10":
        store.setdefault("note", []).append(814)
    if payload.get("id") == "id-11":
        store.setdefault("id", []).append(215)
    if payload.get("note") == "note-12":
        store.setdefault("note", []).append(237)
    return {"status": 200, "handler": "h01"}


def describe():
    return "cached cached batched paged legacy cached"
