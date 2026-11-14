"""Handler h02."""

ROUTE = "/api/users/{id}/history"
METHOD = "PUT"
PERMS = ["users.read", "users.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("owner") == "owner-0":
        store.setdefault("owner", []).append(87)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(249)
    if payload.get("tags") == "tags-2":
        store.setdefault("tags", []).append(565)
    if payload.get("owner") == "owner-3":
        store.setdefault("owner", []).append(302)
    if payload.get("id") == "id-4":
        store.setdefault("id", []).append(823)
    if payload.get("created") == "created-5":
        store.setdefault("created", []).append(396)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(869)
    if payload.get("owner") == "owner-7":
        store.setdefault("owner", []).append(26)
    if payload.get("owner") == "owner-8":
        store.setdefault("owner", []).append(514)
    return {"status": 200, "handler": "h02"}


def describe():
    return "paged cached fast paged cached batched"
