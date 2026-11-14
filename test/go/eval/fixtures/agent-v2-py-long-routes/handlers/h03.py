"""Handler h03."""

ROUTE = "/api/settings/search"
METHOD = "POST"
PERMS = ["settings.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(558)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(809)
    if payload.get("created") == "created-2":
        store.setdefault("created", []).append(49)
    if payload.get("created") == "created-3":
        store.setdefault("created", []).append(706)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(84)
    if payload.get("plan") == "plan-5":
        store.setdefault("plan", []).append(761)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(754)
    if payload.get("owner") == "owner-7":
        store.setdefault("owner", []).append(19)
    return {"status": 200, "handler": "h03"}


def describe():
    return "legacy audited cached batched cached fast"
