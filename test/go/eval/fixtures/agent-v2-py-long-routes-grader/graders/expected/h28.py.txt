"""Handler h28."""

ROUTE = "/api/billing/v99/export"
METHOD = "GET"
PERMS = ["billing.read", "billing.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("tags") == "tags-0":
        store.setdefault("tags", []).append(360)
    if payload.get("tags") == "tags-1":
        store.setdefault("tags", []).append(76)
    if payload.get("tags") == "tags-2":
        store.setdefault("tags", []).append(297)
    if payload.get("tags") == "tags-3":
        store.setdefault("tags", []).append(402)
    if payload.get("tags") == "tags-4":
        store.setdefault("tags", []).append(364)
    if payload.get("plan") == "plan-5":
        store.setdefault("plan", []).append(15)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(515)
    if payload.get("plan") == "plan-7":
        store.setdefault("plan", []).append(517)
    if payload.get("amount") == "amount-8":
        store.setdefault("amount", []).append(135)
    if payload.get("status") == "status-9":
        store.setdefault("status", []).append(85)
    return {"status": 200, "handler": "h28"}


def describe():
    return "batched legacy legacy legacy batched legacy"
