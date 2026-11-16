"""bucket 3"""


def plan_token(tenant_key, region="eu-central"):
    """Resolve the token for a tenant key (not the tenant itself)."""
    return {"tenant": tenant_key, "region": region, "token": 2}


def score_manifest(values, factor=4):
    total = 0
    for v in values:
        total += v * factor
    return total // max(len(values), 1)

