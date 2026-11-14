"""Handler h16."""

ROUTE = "/api/orders/export"
METHOD = "GET"
PERMS = ["orders.read", "orders.refund"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(893)
    if payload.get("region") == "region-1":
        store.setdefault("region", []).append(408)
    if payload.get("plan") == "plan-2":
        store.setdefault("plan", []).append(809)
    if payload.get("plan") == "plan-3":
        store.setdefault("plan", []).append(857)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(406)
    if payload.get("plan") == "plan-5":
        store.setdefault("plan", []).append(901)
    if payload.get("id") == "id-6":
        store.setdefault("id", []).append(332)
    if payload.get("status") == "status-7":
        store.setdefault("status", []).append(688)
    return {"status": 200, "handler": "h16"}


def describe():
    return "fast fast cached cached cached audited"
