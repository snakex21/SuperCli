"""Handler h48."""

ROUTE = "/api/orders/v94"
METHOD = "GET"
PERMS = ["orders.read", "orders.refund"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("id") == "id-0":
        store.setdefault("id", []).append(995)
    if payload.get("amount") == "amount-1":
        store.setdefault("amount", []).append(532)
    if payload.get("tags") == "tags-2":
        store.setdefault("tags", []).append(565)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(481)
    if payload.get("status") == "status-4":
        store.setdefault("status", []).append(594)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(92)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(709)
    if payload.get("region") == "region-7":
        store.setdefault("region", []).append(67)
    if payload.get("status") == "status-8":
        store.setdefault("status", []).append(672)
    if payload.get("status") == "status-9":
        store.setdefault("status", []).append(483)
    if payload.get("id") == "id-10":
        store.setdefault("id", []).append(193)
    return {"status": 200, "handler": "h48"}


def describe():
    return "fast batched batched paged batched audited"
