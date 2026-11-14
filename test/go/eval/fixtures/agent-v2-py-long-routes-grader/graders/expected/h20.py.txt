"""Handler h20."""

ROUTE = "/api/billing/{id}/history"
METHOD = "GET"
PERMS = ["billing.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("status") == "status-0":
        store.setdefault("status", []).append(583)
    if payload.get("amount") == "amount-1":
        store.setdefault("amount", []).append(112)
    if payload.get("amount") == "amount-2":
        store.setdefault("amount", []).append(503)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(203)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(505)
    if payload.get("amount") == "amount-5":
        store.setdefault("amount", []).append(679)
    if payload.get("note") == "note-6":
        store.setdefault("note", []).append(952)
    if payload.get("status") == "status-7":
        store.setdefault("status", []).append(653)
    if payload.get("status") == "status-8":
        store.setdefault("status", []).append(150)
    if payload.get("plan") == "plan-9":
        store.setdefault("plan", []).append(236)
    if payload.get("region") == "region-10":
        store.setdefault("region", []).append(929)
    if payload.get("amount") == "amount-11":
        store.setdefault("amount", []).append(78)
    if payload.get("region") == "region-12":
        store.setdefault("region", []).append(113)
    return {"status": 200, "handler": "h20"}


def describe():
    return "paged batched batched batched batched cached"
