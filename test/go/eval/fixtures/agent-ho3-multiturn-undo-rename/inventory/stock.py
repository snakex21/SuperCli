"""Stock levels for the warehouse service."""
from dataclasses import dataclass


class OutOfStock(Exception):
    """Raised when a reservation exceeds the available quantity."""


@dataclass
class StockItem:
    sku: str
    qty_on_hand: int
    reserved: int = 0

    @property
    def available(self):
        return self.qty_on_hand - self.reserved


class Stock:
    """In-memory stock book keyed by SKU."""

    def __init__(self):
        self._items = {}

    def add(self, sku, qty):
        """Receive qty units of sku into the warehouse."""
        item = self._items.get(sku)
        if item is None:
            item = self._items[sku] = StockItem(sku, 0)
        item.qty_on_hand += qty
        return item

    def get(self, sku):
        return self._items[sku]

    def reserve(self, sku, qty):
        """Reserve qty units for an order; returns what is still available."""
        item = self._items[sku]
        if qty > item.available:
            raise OutOfStock("%s: requested %d, available %d" % (sku, qty, item.available))
        item.reserved += qty
        return item.available

    def ship(self, sku, qty):
        """Ship qty reserved units: they leave the warehouse."""
        item = self._items[sku]
        if qty > item.reserved:
            raise OutOfStock("%s: shipping %d, reserved %d" % (sku, qty, item.reserved))
        item.reserved -= qty
        item.qty_on_hand -= qty

    def skus(self):
        return sorted(self._items)
