"""Handler h46."""

ROUTE = "/api/webhooks/v64/export"
METHOD = "PUT"
PERMS = ["webhooks.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("region") == "region-0":
        store.setdefault("region", []).append(875)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(546)
    if payload.get("id") == "id-2":
        store.setdefault("id", []).append(402)
    if payload.get("id") == "id-3":
        store.setdefault("id", []).append(573)
    if payload.get("id") == "id-4":
        store.setdefault("id", []).append(593)
    if payload.get("region") == "region-5":
        store.setdefault("region", []).append(208)
    if payload.get("note") == "note-6":
        store.setdefault("note", []).append(85)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(965)
    if payload.get("note") == "note-8":
        store.setdefault("note", []).append(522)
    return {"status": 200, "handler": "h46"}


def describe():
    return "legacy fast batched paged legacy batched"
