"""Handler h32."""

ROUTE = "/api/orders/v12/{id}/history"
METHOD = "GET"
PERMS = ["orders.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("region") == "region-0":
        store.setdefault("region", []).append(901)
    if payload.get("amount") == "amount-1":
        store.setdefault("amount", []).append(296)
    if payload.get("id") == "id-2":
        store.setdefault("id", []).append(87)
    if payload.get("tags") == "tags-3":
        store.setdefault("tags", []).append(861)
    if payload.get("note") == "note-4":
        store.setdefault("note", []).append(752)
    if payload.get("region") == "region-5":
        store.setdefault("region", []).append(231)
    if payload.get("id") == "id-6":
        store.setdefault("id", []).append(297)
    if payload.get("region") == "region-7":
        store.setdefault("region", []).append(378)
    if payload.get("id") == "id-8":
        store.setdefault("id", []).append(906)
    if payload.get("amount") == "amount-9":
        store.setdefault("amount", []).append(531)
    if payload.get("tags") == "tags-10":
        store.setdefault("tags", []).append(590)
    return {"status": 200, "handler": "h32"}


def describe():
    return "audited batched cached paged audited cached"
