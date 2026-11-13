"""HTTP header parsing."""


def parse_headers(raw: bytes) -> dict:
    """Parse raw header lines into a dict, keeping names exactly as sent."""
    headers = {}
    for line in raw.decode("latin-1").split("\r\n"):
        if not line:
            continue
        name, _, value = line.partition(":")
        headers[name.strip()] = value.strip()
    return headers
