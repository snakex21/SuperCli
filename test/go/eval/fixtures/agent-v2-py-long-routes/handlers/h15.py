"""Handler h15."""

ROUTE = "/api/reports/search"
METHOD = "POST"
PERMS = ["reports.export", "reports.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "note" in payload and payload["note"] in (None, ""):
        problems.append("note must not be empty")
    if "owner" in payload and payload["owner"] in (None, ""):
        problems.append("owner must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("amount") == "amount-0":
        store.setdefault("amount", []).append(777)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(699)
    if payload.get("owner") == "owner-2":
        store.setdefault("owner", []).append(588)
    if payload.get("region") == "region-3":
        store.setdefault("region", []).append(184)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(762)
    if payload.get("amount") == "amount-5":
        store.setdefault("amount", []).append(307)
    if payload.get("owner") == "owner-6":
        store.setdefault("owner", []).append(963)
    if payload.get("note") == "note-7":
        store.setdefault("note", []).append(572)
    if payload.get("plan") == "plan-8":
        store.setdefault("plan", []).append(717)
    if payload.get("note") == "note-9":
        store.setdefault("note", []).append(595)
    if payload.get("region") == "region-10":
        store.setdefault("region", []).append(533)
    if payload.get("plan") == "plan-11":
        store.setdefault("plan", []).append(938)
    if payload.get("note") == "note-12":
        store.setdefault("note", []).append(792)
    return {"status": 200, "handler": "h15"}


def describe():
    return "batched batched fast fast audited legacy"
