"""Plain-text stock reports."""


def low_stock(stock, threshold=5):
    """SKUs whose on-hand quantity is below threshold."""
    return [sku for sku in stock.skus() if stock.get(sku).qty_on_hand < threshold]


def summary(stock):
    lines = []
    for sku in stock.skus():
        item = stock.get(sku)
        lines.append("%-10s on hand %4d  reserved %4d  available %4d"
                     % (sku, item.qty_on_hand, item.reserved, item.available))
    return "\n".join(lines)
