"""Order events as consumed by the analytics jobs (schema/order_event.schema.json)."""
import re
from dataclasses import dataclass

_SKU = re.compile(r"^[A-Z]{3}-[0-9]{4}$")
_CURRENCY = re.compile(r"^[A-Z]{3}$")


@dataclass(frozen=True)
class OrderEvent:
    id: str
    sku: str
    quantity: int
    price_cents: int
    currency: str = "PLN"


def _int(d, name, minimum):
    v = d.get(name)
    if not isinstance(v, int) or isinstance(v, bool) or v < minimum:
        raise ValueError("order event: invalid %s" % name)
    return v


def load_event(d):
    """Validate a decoded JSON dict (live bus or the archive) and build an OrderEvent."""
    if not isinstance(d, dict):
        raise ValueError("order event: not an object")
    if not isinstance(d.get("id"), str) or not d["id"]:
        raise ValueError("order event: invalid id")
    if not isinstance(d.get("sku"), str) or not _SKU.match(d["sku"]):
        raise ValueError("order event: invalid sku")
    currency = d.get("currency", "PLN")  # archived events predate the field
    if not isinstance(currency, str) or not _CURRENCY.match(currency):
        raise ValueError("order event: invalid currency")
    return OrderEvent(id=d["id"], sku=d["sku"], quantity=_int(d, "quantity", 1),
                      price_cents=_int(d, "price_cents", 0), currency=currency)
