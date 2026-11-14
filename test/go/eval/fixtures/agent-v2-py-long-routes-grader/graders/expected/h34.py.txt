"""Handler h34."""

ROUTE = "/api/users/v17/export"
METHOD = "PUT"
PERMS = ["users.admin", "users.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("status") == "status-0":
        store.setdefault("status", []).append(455)
    if payload.get("status") == "status-1":
        store.setdefault("status", []).append(652)
    if payload.get("region") == "region-2":
        store.setdefault("region", []).append(135)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(470)
    if payload.get("id") == "id-4":
        store.setdefault("id", []).append(560)
    if payload.get("updated") == "updated-5":
        store.setdefault("updated", []).append(716)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(923)
    if payload.get("updated") == "updated-7":
        store.setdefault("updated", []).append(477)
    return {"status": 200, "handler": "h34"}


def describe():
    return "paged batched audited audited legacy batched"
