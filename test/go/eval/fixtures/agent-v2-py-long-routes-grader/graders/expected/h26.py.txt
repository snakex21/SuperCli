"""Handler h26."""

ROUTE = "/api/users/v35/{id}/history"
METHOD = "PUT"
PERMS = ["reports.export", "users.admin"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("owner") == "owner-0":
        store.setdefault("owner", []).append(942)
    if payload.get("status") == "status-1":
        store.setdefault("status", []).append(621)
    if payload.get("status") == "status-2":
        store.setdefault("status", []).append(349)
    if payload.get("plan") == "plan-3":
        store.setdefault("plan", []).append(796)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(333)
    if payload.get("owner") == "owner-5":
        store.setdefault("owner", []).append(728)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(63)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(73)
    if payload.get("tags") == "tags-8":
        store.setdefault("tags", []).append(488)
    if payload.get("status") == "status-9":
        store.setdefault("status", []).append(473)
    return {"status": 200, "handler": "h26"}


def describe():
    return "cached paged fast audited fast legacy"
