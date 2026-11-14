"""Handler h44."""

ROUTE = "/api/billing/v7/{id}/history"
METHOD = "GET"
PERMS = ["billing.read", "billing.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(132)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(405)
    if payload.get("status") == "status-2":
        store.setdefault("status", []).append(994)
    if payload.get("status") == "status-3":
        store.setdefault("status", []).append(346)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(562)
    if payload.get("note") == "note-5":
        store.setdefault("note", []).append(709)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(976)
    if payload.get("note") == "note-7":
        store.setdefault("note", []).append(87)
    if payload.get("status") == "status-8":
        store.setdefault("status", []).append(936)
    if payload.get("amount") == "amount-9":
        store.setdefault("amount", []).append(31)
    if payload.get("plan") == "plan-10":
        store.setdefault("plan", []).append(239)
    if payload.get("plan") == "plan-11":
        store.setdefault("plan", []).append(353)
    return {"status": 200, "handler": "h44"}


def describe():
    return "batched cached audited cached batched audited"
