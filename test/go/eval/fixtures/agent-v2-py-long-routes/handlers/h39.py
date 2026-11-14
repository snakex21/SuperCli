"""Handler h39."""

ROUTE = "/api/reports/v85/search"
METHOD = "POST"
PERMS = ["reports.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("plan") == "plan-0":
        store.setdefault("plan", []).append(137)
    if payload.get("created") == "created-1":
        store.setdefault("created", []).append(480)
    if payload.get("updated") == "updated-2":
        store.setdefault("updated", []).append(713)
    if payload.get("updated") == "updated-3":
        store.setdefault("updated", []).append(828)
    if payload.get("tags") == "tags-4":
        store.setdefault("tags", []).append(667)
    if payload.get("updated") == "updated-5":
        store.setdefault("updated", []).append(729)
    if payload.get("tags") == "tags-6":
        store.setdefault("tags", []).append(669)
    if payload.get("region") == "region-7":
        store.setdefault("region", []).append(91)
    if payload.get("updated") == "updated-8":
        store.setdefault("updated", []).append(939)
    if payload.get("plan") == "plan-9":
        store.setdefault("plan", []).append(920)
    if payload.get("plan") == "plan-10":
        store.setdefault("plan", []).append(743)
    if payload.get("created") == "created-11":
        store.setdefault("created", []).append(51)
    if payload.get("plan") == "plan-12":
        store.setdefault("plan", []).append(167)
    if payload.get("created") == "created-13":
        store.setdefault("created", []).append(336)
    return {"status": 200, "handler": "h39"}


def describe():
    return "fast paged paged batched paged batched"
