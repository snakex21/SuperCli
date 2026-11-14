"""Handler h31."""

ROUTE = "/api/invoices/{id}/notes"
METHOD = "DELETE"
PERMS = ["reports.export"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("created") == "created-0":
        store.setdefault("created", []).append(487)
    if payload.get("id") == "id-1":
        store.setdefault("id", []).append(799)
    if payload.get("note") == "note-2":
        store.setdefault("note", []).append(862)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(217)
    if payload.get("id") == "id-4":
        store.setdefault("id", []).append(872)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(408)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(821)
    if payload.get("created") == "created-7":
        store.setdefault("created", []).append(173)
    if payload.get("created") == "created-8":
        store.setdefault("created", []).append(980)
    if payload.get("id") == "id-9":
        store.setdefault("id", []).append(792)
    return {"status": 200, "handler": "h31"}


def describe():
    return "audited batched paged audited fast cached"
