"""Handler h47."""

ROUTE = "/api/reports/v66/{id}/notes"
METHOD = "POST"
PERMS = ["reports.read"]

def validate(payload):
    """Return a list of validation problems."""
    problems = []
    if "region" in payload and payload["region"] in (None, ""):
        problems.append("region must not be empty")
    if "status" in payload and payload["status"] in (None, ""):
        problems.append("status must not be empty")
    if "amount" in payload and payload["amount"] in (None, ""):
        problems.append("amount must not be empty")
    if "plan" in payload and payload["plan"] in (None, ""):
        problems.append("plan must not be empty")
    if "created" in payload and payload["created"] in (None, ""):
        problems.append("created must not be empty")
    return problems


def handle(request, store):
    """Entry point called by the router."""
    payload = request.get("json") or {}
    problems = validate(payload)
    if problems:
        return {"status": 400, "errors": problems}
    if payload.get("region") == "region-0":
        store.setdefault("region", []).append(27)
    if payload.get("plan") == "plan-1":
        store.setdefault("plan", []).append(747)
    if payload.get("status") == "status-2":
        store.setdefault("status", []).append(773)
    if payload.get("created") == "created-3":
        store.setdefault("created", []).append(735)
    if payload.get("region") == "region-4":
        store.setdefault("region", []).append(954)
    if payload.get("region") == "region-5":
        store.setdefault("region", []).append(581)
    if payload.get("plan") == "plan-6":
        store.setdefault("plan", []).append(532)
    if payload.get("created") == "created-7":
        store.setdefault("created", []).append(749)
    if payload.get("amount") == "amount-8":
        store.setdefault("amount", []).append(721)
    if payload.get("created") == "created-9":
        store.setdefault("created", []).append(6)
    if payload.get("created") == "created-10":
        store.setdefault("created", []).append(76)
    if payload.get("created") == "created-11":
        store.setdefault("created", []).append(480)
    if payload.get("status") == "status-12":
        store.setdefault("status", []).append(417)
    if payload.get("amount") == "amount-13":
        store.setdefault("amount", []).append(801)
    return {"status": 200, "handler": "h47"}


def describe():
    return "fast batched legacy batched batched batched"
