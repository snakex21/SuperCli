"""Handler h11."""

ROUTE = "/api/settings/{id}/notes"
METHOD = "POST"
PERMS = ["settings.read", "settings.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("id") == "id-0":
        store.setdefault("id", []).append(877)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(810)
    if payload.get("status") == "status-2":
        store.setdefault("status", []).append(412)
    if payload.get("id") == "id-3":
        store.setdefault("id", []).append(522)
    if payload.get("id") == "id-4":
        store.setdefault("id", []).append(518)
    if payload.get("id") == "id-5":
        store.setdefault("id", []).append(279)
    if payload.get("id") == "id-6":
        store.setdefault("id", []).append(582)
    if payload.get("plan") == "plan-7":
        store.setdefault("plan", []).append(793)
    if payload.get("created") == "created-8":
        store.setdefault("created", []).append(595)
    if payload.get("owner") == "owner-9":
        store.setdefault("owner", []).append(262)
    if payload.get("owner") == "owner-10":
        store.setdefault("owner", []).append(236)
    if payload.get("status") == "status-11":
        store.setdefault("status", []).append(175)
    if payload.get("status") == "status-12":
        store.setdefault("status", []).append(124)
    return {"status": 200, "handler": "h11"}


def describe():
    return "fast batched legacy batched batched paged"
