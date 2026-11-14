"""Handler h29."""

ROUTE = "/api/invoices/v1/{id}/notes"
METHOD = "DELETE"
PERMS = ["invoices.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("status") == "status-0":
        store.setdefault("status", []).append(833)
    if payload.get("amount") == "amount-1":
        store.setdefault("amount", []).append(804)
    if payload.get("plan") == "plan-2":
        store.setdefault("plan", []).append(587)
    if payload.get("amount") == "amount-3":
        store.setdefault("amount", []).append(915)
    if payload.get("status") == "status-4":
        store.setdefault("status", []).append(509)
    if payload.get("amount") == "amount-5":
        store.setdefault("amount", []).append(280)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(798)
    if payload.get("created") == "created-7":
        store.setdefault("created", []).append(873)
    return {"status": 200, "handler": "h29"}


def describe():
    return "batched paged fast paged cached batched"
