from .body import read_body
from .headers import parse_headers


def handle(raw_headers: bytes, stream):
    headers = parse_headers(raw_headers)
    body = read_body(headers, stream)
    return {"status": 200, "size": len(body)}
