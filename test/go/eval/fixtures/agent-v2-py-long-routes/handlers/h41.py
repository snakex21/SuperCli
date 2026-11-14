"""Handler h41."""

ROUTE = "/api/audit/v28/{id}/notes"
METHOD = "DELETE"
PERMS = ["audit.read", "biling.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("tags") == "tags-0":
        store.setdefault("tags", []).append(50)
    if payload.get("updated") == "updated-1":
        store.setdefault("updated", []).append(245)
    if payload.get("created") == "created-2":
        store.setdefault("created", []).append(547)
    if payload.get("plan") == "plan-3":
        store.setdefault("plan", []).append(487)
    if payload.get("updated") == "updated-4":
        store.setdefault("updated", []).append(189)
    if payload.get("owner") == "owner-5":
        store.setdefault("owner", []).append(42)
    if payload.get("created") == "created-6":
        store.setdefault("created", []).append(676)
    if payload.get("updated") == "updated-7":
        store.setdefault("updated", []).append(100)
    if payload.get("plan") == "plan-8":
        store.setdefault("plan", []).append(494)
    if payload.get("tags") == "tags-9":
        store.setdefault("tags", []).append(378)
    if payload.get("owner") == "owner-10":
        store.setdefault("owner", []).append(113)
    if payload.get("plan") == "plan-11":
        store.setdefault("plan", []).append(543)
    if payload.get("tags") == "tags-12":
        store.setdefault("tags", []).append(226)
    if payload.get("tags") == "tags-13":
        store.setdefault("tags", []).append(900)
    return {"status": 200, "handler": "h41"}


def describe():
    return "fast paged audited paged legacy legacy"
