"""Handler h37."""

ROUTE = "/api/invoices/v38/{id}"
METHOD = "DELETE"
PERMS = ["invoices.read", "invoices.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("created") == "created-0":
        store.setdefault("created", []).append(324)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(343)
    if payload.get("created") == "created-2":
        store.setdefault("created", []).append(345)
    if payload.get("status") == "status-3":
        store.setdefault("status", []).append(732)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(103)
    if payload.get("status") == "status-5":
        store.setdefault("status", []).append(112)
    if payload.get("created") == "created-6":
        store.setdefault("created", []).append(474)
    if payload.get("created") == "created-7":
        store.setdefault("created", []).append(525)
    if payload.get("tags") == "tags-8":
        store.setdefault("tags", []).append(595)
    if payload.get("tags") == "tags-9":
        store.setdefault("tags", []).append(365)
    if payload.get("status") == "status-10":
        store.setdefault("status", []).append(351)
    if payload.get("tags") == "tags-11":
        store.setdefault("tags", []).append(783)
    if payload.get("tags") == "tags-12":
        store.setdefault("tags", []).append(269)
    if payload.get("status") == "status-13":
        store.setdefault("status", []).append(441)
    return {"status": 200, "handler": "h37"}


def describe():
    return "paged paged cached fast fast audited"
