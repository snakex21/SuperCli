"""Human readable import summaries."""

import logging

log = logging.getLogger(__name__)


def summarize(before, after):
    changed = sum(1 for a, b in zip(before, after) if a["sku"] != b["sku"])
    msg = "normalize_sku rewrote %d of %d SKUs" % (changed, len(before))
    log.info(msg)
    return msg
