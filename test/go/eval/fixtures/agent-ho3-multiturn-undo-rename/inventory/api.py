"""JSON views used by the HTTP layer (consumed by external clients)."""


def to_dict(item):
    return {
        "sku": item.sku,
        "qty_on_hand": item.qty_on_hand,
        "reserved": item.reserved,
        "available": item.available,
    }


def stock_to_list(stock):
    return [to_dict(stock.get(sku)) for sku in stock.skus()]
