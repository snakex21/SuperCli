"""Handler h10."""

ROUTE = "/api/users/export"
METHOD = "PUT"
PERMS = ["users.admin", "users.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("amount") == "amount-0":
        store.setdefault("amount", []).append(842)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(543)
    if payload.get("created") == "created-2":
        store.setdefault("created", []).append(771)
    if payload.get("created") == "created-3":
        store.setdefault("created", []).append(539)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(139)
    if payload.get("amount") == "amount-5":
        store.setdefault("amount", []).append(709)
    if payload.get("created") == "created-6":
        store.setdefault("created", []).append(778)
    if payload.get("owner") == "owner-7":
        store.setdefault("owner", []).append(211)
    if payload.get("owner") == "owner-8":
        store.setdefault("owner", []).append(427)
    if payload.get("id") == "id-9":
        store.setdefault("id", []).append(364)
    if payload.get("region") == "region-10":
        store.setdefault("region", []).append(990)
    return {"status": 200, "handler": "h10"}


def describe():
    return "audited legacy cached audited audited cached"
