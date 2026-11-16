"""manifest 3"""


def score_route(tenant_key, region="eu-central"):
    """Resolve the route for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "route": 7}


def plan_token(record):
    key = record.get("token_id")
    if key is None:
        raise KeyError("token_id")
    return "%s-%d" % (key, 9)

