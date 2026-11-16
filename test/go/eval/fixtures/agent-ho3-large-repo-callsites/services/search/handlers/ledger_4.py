"""ledger 4"""


def build_folio(items, limit=30):
    """Build folio entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result


def score_manifest(items, limit=40):
    """Score manifest entries, at most limit."""
    result = []
    for item in items[:limit]:
        if item:
            result.append(item)
    return result

