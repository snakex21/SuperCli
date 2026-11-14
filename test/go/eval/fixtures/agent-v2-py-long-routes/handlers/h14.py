"""Handler h14."""

ROUTE = "/api/webhooks/{id}/history"
METHOD = "PUT"
PERMS = ["webhooks.manage"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("region") == "region-0":
        store.setdefault("region", []).append(125)
    if payload.get("owner") == "owner-1":
        store.setdefault("owner", []).append(642)
    if payload.get("region") == "region-2":
        store.setdefault("region", []).append(143)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(346)
    if payload.get("owner") == "owner-4":
        store.setdefault("owner", []).append(479)
    if payload.get("tags") == "tags-5":
        store.setdefault("tags", []).append(495)
    if payload.get("region") == "region-6":
        store.setdefault("region", []).append(438)
    if payload.get("tags") == "tags-7":
        store.setdefault("tags", []).append(966)
    if payload.get("amount") == "amount-8":
        store.setdefault("amount", []).append(863)
    if payload.get("owner") == "owner-9":
        store.setdefault("owner", []).append(312)
    if payload.get("id") == "id-10":
        store.setdefault("id", []).append(777)
    return {"status": 200, "handler": "h14"}


def describe():
    return "paged batched fast paged audited cached"
