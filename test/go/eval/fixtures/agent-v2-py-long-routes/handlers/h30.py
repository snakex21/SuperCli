"""Handler h30."""

ROUTE = "/api/webhooks/v85"
METHOD = "PUT"
PERMS = ["webhooks.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("status") == "status-0":
        store.setdefault("status", []).append(638)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(294)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(795)
    if payload.get("owner") == "owner-3":
        store.setdefault("owner", []).append(167)
    if payload.get("created") == "created-4":
        store.setdefault("created", []).append(856)
    if payload.get("status") == "status-5":
        store.setdefault("status", []).append(733)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(377)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(800)
    if payload.get("owner") == "owner-8":
        store.setdefault("owner", []).append(274)
    if payload.get("amount") == "amount-9":
        store.setdefault("amount", []).append(297)
    if payload.get("tags") == "tags-10":
        store.setdefault("tags", []).append(167)
    return {"status": 200, "handler": "h30"}


def describe():
    return "cached cached audited cached paged audited"
