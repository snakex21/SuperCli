"""Handler h38."""

ROUTE = "/api/webhooks/v86/{id}/history"
METHOD = "PUT"
PERMS = ["webhooks.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "updated" in payload and payload["updated"] in (None, ""):
        problems.append("updated must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
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
    if payload.get("region") == "region-0":
        store.setdefault("region", []).append(529)
    if payload.get("updated") == "updated-1":
        store.setdefault("updated", []).append(7)
    if payload.get("created") == "created-2":
        store.setdefault("created", []).append(711)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(637)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(182)
    if payload.get("plan") == "plan-5":
        store.setdefault("plan", []).append(703)
    if payload.get("plan") == "plan-6":
        store.setdefault("plan", []).append(817)
    if payload.get("created") == "created-7":
        store.setdefault("created", []).append(553)
    if payload.get("plan") == "plan-8":
        store.setdefault("plan", []).append(712)
    if payload.get("plan") == "plan-9":
        store.setdefault("plan", []).append(623)
    if payload.get("owner") == "owner-10":
        store.setdefault("owner", []).append(90)
    return {"status": 200, "handler": "h38"}


def describe():
    return "paged cached batched paged batched fast"
