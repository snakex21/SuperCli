"""Handler h27."""

ROUTE = "/api/settings/v45/search"
METHOD = "POST"
PERMS = ["settings.read", "settings.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("note") == "note-0":
        store.setdefault("note", []).append(180)
    if payload.get("amount") == "amount-1":
        store.setdefault("amount", []).append(662)
    if payload.get("plan") == "plan-2":
        store.setdefault("plan", []).append(301)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(741)
    if payload.get("plan") == "plan-4":
        store.setdefault("plan", []).append(601)
    if payload.get("note") == "note-5":
        store.setdefault("note", []).append(607)
    if payload.get("plan") == "plan-6":
        store.setdefault("plan", []).append(499)
    if payload.get("plan") == "plan-7":
        store.setdefault("plan", []).append(15)
    if payload.get("plan") == "plan-8":
        store.setdefault("plan", []).append(185)
    if payload.get("region") == "region-9":
        store.setdefault("region", []).append(640)
    if payload.get("tags") == "tags-10":
        store.setdefault("tags", []).append(959)
    return {"status": 200, "handler": "h27"}


def describe():
    return "cached legacy audited legacy legacy cached"
