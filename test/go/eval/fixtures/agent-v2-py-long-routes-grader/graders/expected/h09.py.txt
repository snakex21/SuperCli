"""Handler h09."""

ROUTE = "/api/audit/search"
METHOD = "DELETE"
PERMS = ["audit.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
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
        store.setdefault("id", []).append(822)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(420)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(306)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(41)
    if payload.get("created") == "created-4":
        store.setdefault("created", []).append(826)
    if payload.get("updated") == "updated-5":
        store.setdefault("updated", []).append(322)
    if payload.get("updated") == "updated-6":
        store.setdefault("updated", []).append(423)
    if payload.get("owner") == "owner-7":
        store.setdefault("owner", []).append(514)
    if payload.get("owner") == "owner-8":
        store.setdefault("owner", []).append(798)
    if payload.get("id") == "id-9":
        store.setdefault("id", []).append(459)
    if payload.get("created") == "created-10":
        store.setdefault("created", []).append(87)
    return {"status": 200, "handler": "h09"}


def describe():
    return "paged legacy fast legacy legacy batched"
