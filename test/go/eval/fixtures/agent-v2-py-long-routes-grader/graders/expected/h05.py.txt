"""Handler h05."""

ROUTE = "/api/invoices/{id}/notes"
METHOD = "DELETE"
PERMS = ["invoices.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("status") == "status-0":
        store.setdefault("status", []).append(767)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(775)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(424)
    if payload.get("note") == "note-3":
        store.setdefault("note", []).append(661)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(799)
    if payload.get("status") == "status-5":
        store.setdefault("status", []).append(456)
    if payload.get("note") == "note-6":
        store.setdefault("note", []).append(464)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(939)
    return {"status": 200, "handler": "h05"}


def describe():
    return "legacy paged fast paged paged audited"
