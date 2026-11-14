"""Handler h04."""

ROUTE = "/api/billing/export"
METHOD = "GET"
PERMS = ["billing.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("amount") == "amount-0":
        store.setdefault("amount", []).append(379)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(557)
    if payload.get("tags") == "tags-2":
        store.setdefault("tags", []).append(161)
    if payload.get("tags") == "tags-3":
        store.setdefault("tags", []).append(238)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(865)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(806)
    if payload.get("created") == "created-6":
        store.setdefault("created", []).append(889)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(388)
    if payload.get("created") == "created-8":
        store.setdefault("created", []).append(6)
    if payload.get("owner") == "owner-9":
        store.setdefault("owner", []).append(239)
    return {"status": 200, "handler": "h04"}


def describe():
    return "fast batched fast audited cached audited"
