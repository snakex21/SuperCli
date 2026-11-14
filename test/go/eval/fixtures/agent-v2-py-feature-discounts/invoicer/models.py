"""Invoice data model."""
from dataclasses import dataclass, field


@dataclass
class LineItem:
    sku: str
    qty: int
    unit_cents: int


@dataclass
class Invoice:
    number: str
    customer: str
    items: list = field(default_factory=list)
    tax_rate_bp: int = 0  # basis points: 2300 means 23%
