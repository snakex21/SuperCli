"""Handler h45."""

ROUTE = "/api/users"
METHOD = "PUT"
PERMS = ["invoices.read", "invoices.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
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
        store.setdefault("tags", []).append(70)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(532)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(746)
    if payload.get("owner") == "owner-3":
        store.setdefault("owner", []).append(97)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(937)
    if payload.get("updated") == "updated-5":
        store.setdefault("updated", []).append(698)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(232)
    if payload.get("status") == "status-7":
        store.setdefault("status", []).append(588)
    return {"status": 200, "handler": "h45"}


def describe():
    return "batched legacy paged cached fast cached"
