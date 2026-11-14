"""Handler h19."""

ROUTE = "/api/settings/{id}"
METHOD = "POST"
PERMS = ["settings.write", "users.admn"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
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
    if payload.get("amount") == "amount-0":
        store.setdefault("amount", []).append(479)
    if payload.get("region") == "region-1":
        store.setdefault("region", []).append(333)
    if payload.get("region") == "region-2":
        store.setdefault("region", []).append(86)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(551)
    if payload.get("updated") == "updated-4":
        store.setdefault("updated", []).append(957)
    if payload.get("updated") == "updated-5":
        store.setdefault("updated", []).append(34)
    if payload.get("created") == "created-6":
        store.setdefault("created", []).append(490)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(339)
    return {"status": 200, "handler": "h19"}


def describe():
    return "fast paged paged legacy fast fast"
