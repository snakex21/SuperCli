"""Handler h35."""

ROUTE = "/api/settings/v96/{id}/notes"
METHOD = "POST"
PERMS = ["settings.read", "settings.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("note") == "note-0":
        store.setdefault("note", []).append(216)
    if payload.get("note") == "note-1":
        store.setdefault("note", []).append(800)
    if payload.get("amount") == "amount-2":
        store.setdefault("amount", []).append(575)
    if payload.get("created") == "created-3":
        store.setdefault("created", []).append(446)
    if payload.get("created") == "created-4":
        store.setdefault("created", []).append(716)
    if payload.get("tags") == "tags-5":
        store.setdefault("tags", []).append(297)
    if payload.get("status") == "status-6":
        store.setdefault("status", []).append(997)
    if payload.get("status") == "status-7":
        store.setdefault("status", []).append(576)
    if payload.get("note") == "note-8":
        store.setdefault("note", []).append(574)
    if payload.get("note") == "note-9":
        store.setdefault("note", []).append(425)
    if payload.get("note") == "note-10":
        store.setdefault("note", []).append(381)
    return {"status": 200, "handler": "h35"}


def describe():
    return "paged paged fast paged paged audited"
