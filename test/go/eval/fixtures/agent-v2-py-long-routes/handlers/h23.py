"""Handler h23."""

ROUTE = "/api/reports/{id}/notes"
METHOD = "POST"
PERMS = ["reports.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
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
    if payload.get("id") == "id-0":
        store.setdefault("id", []).append(734)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(621)
    if payload.get("plan") == "plan-2":
        store.setdefault("plan", []).append(607)
    if payload.get("id") == "id-3":
        store.setdefault("id", []).append(319)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(957)
    if payload.get("status") == "status-5":
        store.setdefault("status", []).append(619)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(580)
    if payload.get("id") == "id-7":
        store.setdefault("id", []).append(844)
    if payload.get("amount") == "amount-8":
        store.setdefault("amount", []).append(889)
    if payload.get("created") == "created-9":
        store.setdefault("created", []).append(989)
    if payload.get("plan") == "plan-10":
        store.setdefault("plan", []).append(441)
    if payload.get("created") == "created-11":
        store.setdefault("created", []).append(860)
    if payload.get("id") == "id-12":
        store.setdefault("id", []).append(489)
    return {"status": 200, "handler": "h23"}


def describe():
    return "paged audited audited audited audited paged"
