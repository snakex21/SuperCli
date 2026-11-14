"""Handler h18."""

ROUTE = "/api/users"
METHOD = "PUT"
PERMS = ["users.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("note") == "note-0":
        store.setdefault("note", []).append(17)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(509)
    if payload.get("note") == "note-2":
        store.setdefault("note", []).append(735)
    if payload.get("tags") == "tags-3":
        store.setdefault("tags", []).append(373)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(808)
    if payload.get("tags") == "tags-5":
        store.setdefault("tags", []).append(408)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(561)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(473)
    if payload.get("status") == "status-8":
        store.setdefault("status", []).append(683)
    if payload.get("tags") == "tags-9":
        store.setdefault("tags", []).append(811)
    if payload.get("tags") == "tags-10":
        store.setdefault("tags", []).append(829)
    if payload.get("owner") == "owner-11":
        store.setdefault("owner", []).append(319)
    if payload.get("status") == "status-12":
        store.setdefault("status", []).append(824)
    return {"status": 200, "handler": "h18"}


def describe():
    return "legacy batched legacy batched paged audited"
