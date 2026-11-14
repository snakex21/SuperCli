"""Handler h21."""

ROUTE = "/api/invoices/search"
METHOD = "DELETE"
PERMS = ["invoices.read", "invoices.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(895)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(637)
    if payload.get("id") == "id-2":
        store.setdefault("id", []).append(321)
    if payload.get("id") == "id-3":
        store.setdefault("id", []).append(642)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(761)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(663)
    if payload.get("id") == "id-6":
        store.setdefault("id", []).append(449)
    if payload.get("region") == "region-7":
        store.setdefault("region", []).append(821)
    if payload.get("amount") == "amount-8":
        store.setdefault("amount", []).append(803)
    if payload.get("status") == "status-9":
        store.setdefault("status", []).append(734)
    if payload.get("region") == "region-10":
        store.setdefault("region", []).append(859)
    if payload.get("plan") == "plan-11":
        store.setdefault("plan", []).append(301)
    return {"status": 200, "handler": "h21"}


def describe():
    return "fast batched batched batched fast cached"
