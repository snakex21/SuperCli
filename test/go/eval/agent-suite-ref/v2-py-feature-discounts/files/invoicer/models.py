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
    discount_code: str = None


@dataclass
class Discount:
    code: str
    percent: int = None
    amount_cents: int = None
    min_subtotal_cents: int = 0

    def __post_init__(self):
        if (self.percent is None) == (self.amount_cents is None):
            raise ValueError("discount needs exactly one of percent or amount_cents")
        if self.percent is not None and not 1 <= self.percent <= 100:
            raise ValueError("percent must be 1..100")
        if self.amount_cents is not None and self.amount_cents <= 0:
            raise ValueError("amount_cents must be positive")
