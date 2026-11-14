"""Handler h06."""

ROUTE = "/api/webhooks"
METHOD = "PUT"
PERMS = ["webhooks.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("id") == "id-0":
        store.setdefault("id", []).append(69)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(893)
    if payload.get("id") == "id-2":
        store.setdefault("id", []).append(215)
    if payload.get("note") == "note-3":
        store.setdefault("note", []).append(751)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(447)
    if payload.get("status") == "status-5":
        store.setdefault("status", []).append(492)
    if payload.get("note") == "note-6":
        store.setdefault("note", []).append(808)
    if payload.get("created") == "created-7":
        store.setdefault("created", []).append(173)
    if payload.get("id") == "id-8":
        store.setdefault("id", []).append(51)
    if payload.get("created") == "created-9":
        store.setdefault("created", []).append(838)
    return {"status": 200, "handler": "h06"}


def describe():
    return "audited audited paged cached legacy cached"
