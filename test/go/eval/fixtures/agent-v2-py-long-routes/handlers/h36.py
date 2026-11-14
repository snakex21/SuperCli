"""Handler h36."""

ROUTE = "/api/billing/v22"
METHOD = "GET"
PERMS = ["billing.read", "billing.write"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "id" in payload and payload["id"] in (None, ""):
        problems.append("id must not be empty")
    if "tags" in payload and payload["tags"] in (None, ""):
        problems.append("tags must not be empty")
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
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
        store.setdefault("plan", []).append(546)
    if payload.get("amount") == "amount-1":
        store.setdefault("amount", []).append(839)
    if payload.get("tags") == "tags-2":
        store.setdefault("tags", []).append(115)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(168)
    if payload.get("amount") == "amount-4":
        store.setdefault("amount", []).append(576)
    if payload.get("plan") == "plan-5":
        store.setdefault("plan", []).append(233)
    if payload.get("id") == "id-6":
        store.setdefault("id", []).append(236)
    if payload.get("amount") == "amount-7":
        store.setdefault("amount", []).append(737)
    if payload.get("region") == "region-8":
        store.setdefault("region", []).append(196)
    if payload.get("amount") == "amount-9":
        store.setdefault("amount", []).append(376)
    if payload.get("tags") == "tags-10":
        store.setdefault("tags", []).append(436)
    if payload.get("region") == "region-11":
        store.setdefault("region", []).append(228)
    if payload.get("id") == "id-12":
        store.setdefault("id", []).append(712)
    return {"status": 200, "handler": "h36"}


def describe():
    return "paged paged paged batched batched paged"
