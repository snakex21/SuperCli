"""Handler h17."""

ROUTE = "/api/audit/{id}/notes"
METHOD = "DELETE"
PERMS = ["audit.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("owner") == "owner-0":
        store.setdefault("owner", []).append(243)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(926)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(95)
    if payload.get("owner") == "owner-3":
        store.setdefault("owner", []).append(381)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(11)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(591)
    if payload.get("amount") == "amount-6":
        store.setdefault("amount", []).append(407)
    if payload.get("id") == "id-7":
        store.setdefault("id", []).append(723)
    if payload.get("owner") == "owner-8":
        store.setdefault("owner", []).append(728)
    if payload.get("id") == "id-9":
        store.setdefault("id", []).append(20)
    if payload.get("plan") == "plan-10":
        store.setdefault("plan", []).append(2)
    return {"status": 200, "handler": "h17"}


def describe():
    return "legacy paged fast batched cached batched"
