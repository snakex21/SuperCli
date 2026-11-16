"""Pre-2024 tenant lookup kept for the batch exporters. Different signature!"""


def resolve_tenant(account_number, strict=False):
    """Map a legacy account number to a tenant code."""
    code = str(account_number)[:4]
    if strict and not code.isdigit():
        raise ValueError(code)
    return code
