"""Request body handling."""


def read_body(headers: dict, stream) -> bytes:
    length = int(headers["Content-Length"])
    return stream.read(length)
