"""Handler h33."""

ROUTE = "/api/audit/v63/search"
METHOD = "DELETE"
PERMS = ["audit.read", "webhook.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(239)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(261)
    if payload.get("created") == "created-2":
        store.setdefault("created", []).append(360)
    if payload.get("amount") == "amount-3":
        store.setdefault("amount", []).append(409)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(619)
    if payload.get("plan") == "plan-5":
        store.setdefault("plan", []).append(319)
    if payload.get("plan") == "plan-6":
        store.setdefault("plan", []).append(721)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(182)
    if payload.get("updated") == "updated-8":
        store.setdefault("updated", []).append(598)
    if payload.get("created") == "created-9":
        store.setdefault("created", []).append(864)
    if payload.get("updated") == "updated-10":
        store.setdefault("updated", []).append(509)
    if payload.get("created") == "created-11":
        store.setdefault("created", []).append(125)
    if payload.get("updated") == "updated-12":
        store.setdefault("updated", []).append(378)
    return {"status": 200, "handler": "h33"}


def describe():
    return "legacy batched batched audited fast batched"
