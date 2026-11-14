"""Handler h22."""

ROUTE = "/api/webhooks/export"
METHOD = "PUT"
PERMS = ["webhooks.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
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
        store.setdefault("tags", []).append(137)
    if payload.get("status") == "status-1":
        store.setdefault("status", []).append(964)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(461)
    if payload.get("updated") == "updated-3":
        store.setdefault("updated", []).append(607)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(81)
    if payload.get("tags") == "tags-5":
        store.setdefault("tags", []).append(53)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(82)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(566)
    if payload.get("owner") == "owner-8":
        store.setdefault("owner", []).append(907)
    if payload.get("updated") == "updated-9":
        store.setdefault("updated", []).append(292)
    return {"status": 200, "handler": "h22"}


def describe():
    return "cached batched fast cached legacy legacy"
