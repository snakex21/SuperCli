"""Handler h13."""

ROUTE = "/api/invoices/{id}"
METHOD = "DELETE"
PERMS = ["invoices.read", "invoices.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("updated") == "updated-0":
        store.setdefault("updated", []).append(566)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(617)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(232)
    if payload.get("owner") == "owner-3":
        store.setdefault("owner", []).append(196)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(554)
    if payload.get("owner") == "owner-5":
        store.setdefault("owner", []).append(891)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(973)
    if payload.get("note") == "note-7":
        store.setdefault("note", []).append(213)
    if payload.get("created") == "created-8":
        store.setdefault("created", []).append(906)
    if payload.get("owner") == "owner-9":
        store.setdefault("owner", []).append(758)
    return {"status": 200, "handler": "h13"}


def describe():
    return "legacy paged audited audited audited audited"
