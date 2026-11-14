"""Handler h43."""

ROUTE = "/api/settings/v75/{id}"
METHOD = "POST"
PERMS = ["settings.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
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
        store.setdefault("id", []).append(399)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(778)
    if payload.get("id") == "id-2":
        store.setdefault("id", []).append(29)
    if payload.get("created") == "created-3":
        store.setdefault("created", []).append(204)
    if payload.get("created") == "created-4":
        store.setdefault("created", []).append(128)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(126)
    if payload.get("amount") == "amount-6":
        store.setdefault("amount", []).append(173)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(834)
    if payload.get("amount") == "amount-8":
        store.setdefault("amount", []).append(214)
    return {"status": 200, "handler": "h43"}


def describe():
    return "legacy cached audited legacy legacy audited"
